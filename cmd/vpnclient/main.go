package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ilya1st/wpn/internal/compression"
	"github.com/ilya1st/wpn/internal/config"
	"github.com/ilya1st/wpn/internal/fragment"
	"github.com/ilya1st/wpn/internal/protocol"
	"github.com/ilya1st/wpn/internal/routes"
	"github.com/ilya1st/wpn/internal/tun"
	"github.com/ilya1st/wpn/internal/ws"
)

func main() {
	configFile := flag.String("config", "client.yaml", "Path to client config file")
	flag.Parse()

	cfg, err := config.LoadClientConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Аутентификация и получение сессии
	authResult, err := authenticateAndConnect(cfg)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}

	log.Printf("Session %s assigned: IP4=%v, IP6=%v", authResult.SessionID, authResult.AssignedIP4, authResult.AssignedIP6)

	// Создание TUN интерфейса с полученными IP
	tunConfig := tun.Config{
		Name: cfg.TUN.Name,
		MTU:  cfg.TUN.MTU,
	}

	// IPv4
	if len(authResult.AssignedIP4) > 0 {
		tunConfig.IP = authResult.AssignedIP4.String()
		tunConfig.Subnet = int(authResult.Subnet4)
	} else if cfg.TUN.IP != "" {
		tunConfig.IP = cfg.TUN.IP
		tunConfig.Subnet = cfg.TUN.Subnet
	} else {
		tunConfig.IP = "10.0.0.2"
		tunConfig.Subnet = 24
	}

	// IPv6 (клиент получает от сервера, свой конфиг не используется)
	if len(authResult.AssignedIP6) > 0 {
		tunConfig.IP6 = authResult.AssignedIP6.String()
		tunConfig.Subnet6 = int(authResult.Subnet6)
	}

	tunIface, err := tun.New(tunConfig)
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()

	if err := tunIface.Setup(); err != nil {
		log.Fatalf("Failed to setup TUN interface: %v", err)
	}

	log.Printf("TUN interface %s ready", tunIface.Name())
	if len(authResult.AssignedIP4) > 0 {
		log.Printf("  IPv4: %s/%d", authResult.AssignedIP4, authResult.Subnet4)
	}
	if len(authResult.AssignedIP6) > 0 {
		log.Printf("  IPv6: %s/%d", authResult.AssignedIP6, authResult.Subnet6)
	}

	// Маршруты
	routeManager := routes.NewManager(cfg.TUN.Name)

	// Мьютекс для WebSocket (tunToWS + keepalive пишут в одно соединение)
	var wsMutex sync.Mutex

	// Циклы обмена данными
	conn := authResult.Conn
	frag := fragment.NewFragmenter()
	assembler := fragment.NewAssembler(cfg.GetFragmentTimeout(), func(fragmentID uint32) {
		log.Printf("Fragment assembly timeout for ID=%d", fragmentID)
	})
	defer assembler.Cleanup()

	go tunToWS(tunIface, conn, cfg, frag, &wsMutex)
	go wsToTUN(tunIface, conn, routeManager, cfg, &wsMutex, assembler)
	go keepalive(conn, cfg, &wsMutex)

	// Ожидание сигнала
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("VPN client connected. Press Ctrl+C to disconnect.")

	<-sigCh
	log.Println("Received shutdown signal")

	disconnectMsg := protocol.CreateDisconnectMessage("Client disconnect")
	wsMutex.Lock()
	conn.WriteMessage(websocket.BinaryMessage, disconnectMsg.Serialize())
	wsMutex.Unlock()

	log.Println("Shutting down...")
}

// AuthResult результат аутентификации
type AuthResult struct {
	SessionID   string
	AssignedIP4 net.IP
	AssignedIP6 net.IP
	Subnet4     byte
	Subnet6     byte
	ServerIP4   net.IP
	ServerIP6   net.IP
	Conn        *websocket.Conn
}

// authenticateAndConnect подключается и проходит аутентификацию
func authenticateAndConnect(cfg *config.ClientConfig) (*AuthResult, error) {
	proxyConfig := (*ws.ProxyConfig)(nil)
	if cfg.Proxy.Enabled {
		proxyConfig = &ws.ProxyConfig{
			Enabled:  true,
			Type:     cfg.Proxy.Type,
			Address:  cfg.Proxy.Address,
			Port:     cfg.Proxy.Port,
			Username: cfg.Proxy.Username,
			Password: cfg.Proxy.Password,
		}
	}

	wsClient := ws.NewClient(ws.ClientConfig{
		ServerURL:          cfg.GetServerURL(),
		Proxy:              proxyConfig,
		TLS:                cfg.Client.UseTLS,
		InsecureSkipVerify: cfg.Client.AllowInsecure,
	})

	// Подключение с повторными попытками
	var conn *websocket.Conn
	for attempt := 1; attempt <= cfg.Connection.MaxReconnects; attempt++ {
		log.Printf("Connecting to server (attempt %d/%d)...", attempt, cfg.Connection.MaxReconnects)

		if err := wsClient.Connect(); err != nil {
			log.Printf("Connection failed: %v", err)
			if attempt < cfg.Connection.MaxReconnects {
				time.Sleep(time.Duration(cfg.Connection.ReconnectDelay) * time.Second)
				continue
			}
			return nil, fmt.Errorf("failed to connect after %d attempts: %w", attempt, err)
		}

		conn = wsClient.Connection()
		break
	}

	// Аутентификация
	result, err := authenticate(conn, cfg)
	if err != nil {
		return nil, err
	}

	result.Conn = conn
	return result, nil
}

// authenticate выполняет аутентификацию и возвращает результат
func authenticate(conn *websocket.Conn, cfg *config.ClientConfig) (*AuthResult, error) {
	// Ожидание AUTH_CHALLENGE
	conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Auth.Timeout) * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read auth challenge: %w", err)
	}

	if msgType != websocket.BinaryMessage {
		return nil, fmt.Errorf("expected binary message, got: %d", msgType)
	}

	msg, err := protocol.DeserializeMessage(data)
	if err != nil {
		return nil, fmt.Errorf("deserialize message: %w", err)
	}

	if msg.Type != protocol.MessageTypeControl {
		return nil, fmt.Errorf("expected control message, got: 0x%02x", msg.Type)
	}

	controlType, err := msg.GetControlType()
	if err != nil {
		return nil, fmt.Errorf("get control type: %w", err)
	}

	if controlType != protocol.ControlTypeAuthChallenge {
		return nil, fmt.Errorf("expected auth challenge, got: 0x%02x", controlType)
	}

	// Отправка AUTH_RESPONSE
	authPayload := protocol.CreateAuthResponsePayload(cfg.Auth.Username, cfg.Auth.Password)
	authMsg := protocol.CreateControlMessage(protocol.ControlTypeAuthResponse, authPayload)

	if err := conn.WriteMessage(websocket.BinaryMessage, authMsg.Serialize()); err != nil {
		return nil, fmt.Errorf("send auth response: %w", err)
	}

	// Ожидание AUTH_SUCCESS/AUTH_FAILURE
	msgType, data, err = conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read auth result: %w", err)
	}

	msg, err = protocol.DeserializeMessage(data)
	if err != nil {
		return nil, fmt.Errorf("deserialize message: %w", err)
	}

	controlType, err = msg.GetControlType()
	if err != nil {
		return nil, fmt.Errorf("get control type: %w", err)
	}

	switch controlType {
	case protocol.ControlTypeAuthSuccess:
		log.Println("Authentication successful")
		payload, _ := msg.GetControlPayload()
		authSuccess, err := protocol.ParseAuthSuccessPayload(payload)
		if err != nil {
			log.Printf("Warning: failed to parse auth success payload: %v", err)
			return &AuthResult{}, nil
		}

		result := &AuthResult{
			SessionID:   authSuccess.SessionID,
			AssignedIP4: authSuccess.ClientIP4,
			AssignedIP6: authSuccess.ClientIP6,
			Subnet4:     authSuccess.Subnet4,
			Subnet6:     authSuccess.Subnet6,
			ServerIP4:   authSuccess.ServerIP4,
			ServerIP6:   authSuccess.ServerIP6,
		}

		if len(authSuccess.ClientIP4) > 0 {
			log.Printf("Assigned IPv4: %s/%d", authSuccess.ClientIP4, authSuccess.Subnet4)
		}
		if len(authSuccess.ClientIP6) > 0 {
			log.Printf("Assigned IPv6: %s/%d", authSuccess.ClientIP6, authSuccess.Subnet6)
		}

		return result, nil

	case protocol.ControlTypeAuthFailure:
		payload, _ := msg.GetControlPayload()
		reason := string(payload)
		return nil, fmt.Errorf("auth failed: %s", reason)

	default:
		return nil, fmt.Errorf("unexpected message type: 0x%02x", controlType)
	}
}

// tunToWS читает пакеты из TUN и отправляет в WebSocket
func tunToWS(tunIface *tun.Interface, conn *websocket.Conn, cfg *config.ClientConfig, frag *fragment.Fragmenter, mutex *sync.Mutex) {
	buf := make([]byte, 65535)
	for {
		n, err := tunIface.Read(buf)
		if err != nil {
			log.Printf("Failed to read from TUN: %v", err)
			return
		}

		packet := buf[:n]
		isIPv6 := tun.IsIPv6Packet(packet)

		if frag.NeedsFragment(len(packet)) {
			fragMsgs := frag.Fragment(packet, isIPv6)
			for _, msg := range fragMsgs {
				mutex.Lock()
				err = conn.WriteMessage(websocket.BinaryMessage, msg.Serialize())
				mutex.Unlock()
				if err != nil {
					log.Printf("Failed to write fragment: %v", err)
					return
				}
			}
			continue
		}

		if cfg.CompressionEnabled() {
			compressed, err := compression.Compress(packet)
			if err == nil && len(compressed) < len(packet) {
				msg := protocol.CreateDataMessage(compressed, isIPv6)
				msg.Flags |= protocol.FlagCompressed
				mutex.Lock()
				err = conn.WriteMessage(websocket.BinaryMessage, msg.Serialize())
				mutex.Unlock()
				if err != nil {
					log.Printf("Failed to write compressed: %v", err)
					return
				}
				continue
			}
		}

		msg := protocol.CreateDataMessage(packet, isIPv6)
		mutex.Lock()
		err = conn.WriteMessage(websocket.BinaryMessage, msg.Serialize())
		mutex.Unlock()
		if err != nil {
			log.Printf("Failed to write to WebSocket: %v", err)
			return
		}
	}
}

// wsToTUN читает сообщения из WebSocket и записывает в TUN
func wsToTUN(tunIface *tun.Interface, conn *websocket.Conn, routeManager *routes.Manager, cfg *config.ClientConfig, mutex *sync.Mutex, assembler *fragment.Assembler) {
	conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Connection.KeepaliveTimeout) * time.Second))

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read from WebSocket: %v", err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Connection.KeepaliveTimeout) * time.Second))

		if msgType != websocket.BinaryMessage {
			log.Printf("Expected binary message, got: %d", msgType)
			continue
		}

		msg, err := protocol.DeserializeMessage(data)
		if err != nil {
			log.Printf("Failed to deserialize message: %v", err)
			continue
		}

		switch msg.Type {
		case protocol.MessageTypeData:
			payload := msg.Payload
			if msg.Flags&protocol.FlagCompressed != 0 {
				decompressed, err := compression.Decompress(payload)
				if err != nil {
					log.Printf("Failed to decompress packet: %v", err)
					continue
				}
				payload = decompressed
			}
			if _, err := tunIface.Write(payload); err != nil {
				log.Printf("Failed to write to TUN: %v", err)
			}

		case protocol.MessageTypeFragment:
			packet, isIPv6, complete := assembler.HandleFragment(msg)
			if complete {
				payload := packet
				if msg.Flags&protocol.FlagCompressed != 0 {
					decompressed, err := compression.Decompress(payload)
					if err != nil {
						log.Printf("Failed to decompress assembled packet: %v", err)
						continue
					}
					payload = decompressed
				}
				if _, err := tunIface.Write(payload); err != nil {
					log.Printf("Failed to write assembled packet to TUN: %v", err)
				} else {
					log.Printf("Assembled fragment: %d bytes (IPv6=%v)", len(payload), isIPv6)
				}
			}

		case protocol.MessageTypeControl:
			controlType, _ := msg.GetControlType()
			switch controlType {
			case protocol.ControlTypeRoutesConfig:
				serverRoutes, err := parseRoutesFromMessage(msg)
				if err != nil {
					log.Printf("Failed to parse server routes: %v", err)
					continue
				}

				clientRoutes, err := routes.ParseRoutesFromConfig(cfg.Routes, cfg.TUN.Name)
				if err != nil {
					log.Printf("Failed to parse client routes: %v", err)
					continue
				}

				allRoutes := routes.MergeWithServerRoutes(clientRoutes, serverRoutes)
				routeManager.AddRoutes(allRoutes)

				if err := routeManager.ApplyRoutes(); err != nil {
					log.Printf("Failed to apply routes: %v", err)
				} else {
					log.Printf("Routes applied: %d total", len(allRoutes))
				}

			case protocol.ControlTypeDisconnect:
				log.Println("Server requested disconnect")
				return

			default:
				log.Printf("Unhandled control type: 0x%02x", controlType)
			}

		case protocol.MessageTypeKeepalive:
			// Игнорируем

		default:
			log.Printf("Unknown message type: 0x%02x", msg.Type)
		}
	}
}

// keepalive периодически отправляет keepalive сообщения
func keepalive(conn *websocket.Conn, cfg *config.ClientConfig, mutex *sync.Mutex) {
	ticker := time.NewTicker(time.Duration(cfg.Connection.KeepaliveInterval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		keepalive := protocol.CreateKeepaliveMessage()
		mutex.Lock()
		err := conn.WriteMessage(websocket.BinaryMessage, keepalive.Serialize())
		mutex.Unlock()
		if err != nil {
			log.Printf("Failed to send keepalive: %v", err)
			return
		}
	}
}

// parseRoutesFromMessage парсит маршруты из CONTROL сообщения
func parseRoutesFromMessage(msg *protocol.Message) ([]routes.Route, error) {
	payload, err := msg.GetControlPayload()
	if err != nil {
		return nil, fmt.Errorf("get control payload: %w", err)
	}

	if len(payload) < 1 {
		return nil, fmt.Errorf("empty routes payload")
	}

	numRoutes := int(payload[0])
	result := make([]routes.Route, 0, numRoutes)

	offset := 1
	for i := 0; i < numRoutes; i++ {
		if offset >= len(payload) {
			return nil, fmt.Errorf("unexpected end of payload")
		}

		dstLen := int(payload[offset])
		offset++

		if offset+dstLen > len(payload) {
			return nil, fmt.Errorf("invalid dst length")
		}
		dstStr := string(payload[offset : offset+dstLen])
		offset += dstLen

		if offset >= len(payload) {
			return nil, fmt.Errorf("unexpected end of payload")
		}
		gwLen := int(payload[offset])
		offset++

		if offset+gwLen > len(payload) {
			return nil, fmt.Errorf("invalid gw length")
		}
		gwStr := string(payload[offset : offset+gwLen])
		offset += gwLen

		if offset+2 > len(payload) {
			return nil, fmt.Errorf("unexpected end of payload")
		}
		metric := int(payload[offset])
		offset += 2 // metric + reserved

		_, dst, err := net.ParseCIDR(dstStr)
		if err != nil {
			return nil, fmt.Errorf("parse dst %s: %w", dstStr, err)
		}

		var gw net.IP
		if gwStr != "" {
			gw = net.ParseIP(gwStr)
			if gw == nil {
				return nil, fmt.Errorf("invalid gw IP: %s", gwStr)
			}
		}

		result = append(result, routes.Route{
			Dst:    dst,
			GW:     gw,
			Metric: metric,
		})
	}

	return result, nil
}
