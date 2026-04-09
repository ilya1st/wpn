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
	"github.com/iazarov/vpn/internal/config"
	"github.com/iazarov/vpn/internal/protocol"
	"github.com/iazarov/vpn/internal/routes"
	"github.com/iazarov/vpn/internal/tun"
	"github.com/iazarov/vpn/internal/ws"
)

func main() {
	configFile := flag.String("config", "client.yaml", "Path to client config file")
	flag.Parse()

	// Загрузка конфигурации
	cfg, err := config.LoadClientConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Создание TUN интерфейса
	tunConfig := tun.Config{
		Name: cfg.TUN.Name,
		// IP будет установлен после аутентификации
	}

	tunIface, err := tun.New(tunConfig)
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()

	// Создание WebSocket клиента
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
		ServerURL: cfg.GetServerURL(),
		Proxy:     proxyConfig,
		TLS:       cfg.Client.UseTLS,
	})

	// Подключение к серверу с повторными попытками
	var conn *websocket.Conn
	for attempt := 1; attempt <= cfg.Timeouts.MaxReconnects; attempt++ {
		log.Printf("Connecting to server (attempt %d/%d)...", attempt, cfg.Timeouts.MaxReconnects)
		
		if err := wsClient.Connect(); err != nil {
			log.Printf("Connection failed: %v", err)
			if attempt < cfg.Timeouts.MaxReconnects {
				time.Sleep(time.Duration(cfg.Timeouts.ReconnectDelay) * time.Second)
				continue
			}
			log.Fatalf("Failed to connect after %d attempts: %v", attempt, err)
		}

		conn = wsClient.Connection()
		break
	}

	// Аутентификация
	assignedIP, err := authenticate(conn, cfg)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}

	// Настройка TUN интерфейса с полученным IP
	if assignedIP != "" {
		tunConfig.IP = assignedIP
		tunConfig.Subnet = 24
	} else {
		tunConfig.IP = "10.0.0.2" // IP по умолчанию
		tunConfig.Subnet = 24
	}

	// Пересоздаём TUN с новым IP
	tunIface.Close()
	tunIface, err = tun.New(tunConfig)
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()

	if err := tunIface.Setup(); err != nil {
		log.Fatalf("Failed to setup TUN interface: %v", err)
	}

	log.Printf("TUN interface %s ready with IP %s", tunIface.Name(), tunConfig.IP)

	// Настройка маршрутов клиента (имеют приоритет над серверными)
	routeManager := routes.NewManager(cfg.TUN.Name)

	// Мьютекс для синхронизации записей в WebSocket
	var wsMutex sync.Mutex

	// Запуск цикла обмена данными
	go tunToWS(tunIface, conn, &wsMutex)
	go wsToTUN(tunIface, conn, routeManager, cfg, &wsMutex)
	go keepalive(conn, cfg, &wsMutex)

	// Ожидание сигнала
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("VPN client connected. Press Ctrl+C to disconnect.")

	<-sigCh
	log.Println("Received shutdown signal")

	// Отправка DISCONNECT
	disconnectMsg := protocol.CreateDisconnectMessage("Client disconnect")
	conn.WriteMessage(websocket.BinaryMessage, disconnectMsg.Serialize())

	log.Println("Shutting down...")
}

// authenticate выполняет аутентификацию на сервере и возвращает назначенный IP
func authenticate(conn *websocket.Conn, cfg *config.ClientConfig) (string, error) {
	// Ожидание AUTH_CHALLENGE
	conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Auth.Timeout) * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("read auth challenge: %w", err)
	}

	if msgType != websocket.BinaryMessage {
		return "", fmt.Errorf("expected binary message, got: %d", msgType)
	}

	msg, err := protocol.DeserializeMessage(data)
	if err != nil {
		return "", fmt.Errorf("deserialize message: %w", err)
	}

	if msg.Type != protocol.MessageTypeControl {
		return "", fmt.Errorf("expected control message, got: 0x%02x", msg.Type)
	}

	controlType, err := msg.GetControlType()
	if err != nil {
		return "", fmt.Errorf("get control type: %w", err)
	}

	if controlType != protocol.ControlTypeAuthChallenge {
		return "", fmt.Errorf("expected auth challenge, got: 0x%02x", controlType)
	}

	// Отправка AUTH_RESPONSE
	authPayload := protocol.CreateAuthResponsePayload(cfg.Auth.Username, cfg.Auth.Password)
	authMsg := protocol.CreateControlMessage(protocol.ControlTypeAuthResponse, authPayload)

	if err := conn.WriteMessage(websocket.BinaryMessage, authMsg.Serialize()); err != nil {
		return "", fmt.Errorf("send auth response: %w", err)
	}

	// Ожидание AUTH_SUCCESS/AUTH_FAILURE
	msgType, data, err = conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("read auth result: %w", err)
	}

	msg, err = protocol.DeserializeMessage(data)
	if err != nil {
		return "", fmt.Errorf("deserialize message: %w", err)
	}

	controlType, err = msg.GetControlType()
	if err != nil {
		return "", fmt.Errorf("get control type: %w", err)
	}

	switch controlType {
	case protocol.ControlTypeAuthSuccess:
		log.Println("Authentication successful")
		// Получаем IP из payload
		payload, _ := msg.GetControlPayload()
		authSuccess, err := protocol.ParseAuthSuccessPayload(payload)
		if err != nil {
			log.Printf("Warning: failed to parse auth success payload: %v", err)
			return "", nil // Вернём пустой IP (будет IP по умолчанию)
		}
		assignedIP := net.IP(authSuccess.ClientIP).String()
		log.Printf("Assigned IP: %s/%d", assignedIP, authSuccess.Subnet)
		return assignedIP, nil

	case protocol.ControlTypeAuthFailure:
		payload, _ := msg.GetControlPayload()
		reason := string(payload)
		return "", fmt.Errorf("auth failed: %s", reason)

	default:
		return "", fmt.Errorf("unexpected message type: 0x%02x", controlType)
	}
}

// tunToWS читает пакеты из TUN и отправляет в WebSocket
func tunToWS(tunIface *tun.Interface, conn *websocket.Conn, mutex *sync.Mutex) {
	buf := make([]byte, 65535)
	for {
		n, err := tunIface.Read(buf)
		if err != nil {
			log.Printf("Failed to read from TUN: %v", err)
			return
		}

		packet := buf[:n]
		isIPv6 := tun.IsIPv6Packet(packet)

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
func wsToTUN(tunIface *tun.Interface, conn *websocket.Conn, routeManager *routes.Manager, cfg *config.ClientConfig, mutex *sync.Mutex) {
	// Устанавливаем начальный deadline
	conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Timeouts.KeepaliveTimeout) * time.Second))
	
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read from WebSocket: %v", err)
			return
		}

		// Обновляем deadline после получения любого сообщения
		conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Timeouts.KeepaliveTimeout) * time.Second))

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
			if _, err := tunIface.Write(msg.Payload); err != nil {
				log.Printf("Failed to write to TUN: %v", err)
			}

		case protocol.MessageTypeControl:
			controlType, _ := msg.GetControlType()
			switch controlType {
			case protocol.ControlTypeRoutesConfig:
				// Получение маршрутов от сервера
				serverRoutes, err := parseRoutesFromMessage(msg)
				if err != nil {
					log.Printf("Failed to parse server routes: %v", err)
					continue
				}
				
				// Парсим клиентские маршруты
				clientRoutes, err := routes.ParseRoutesFromConfig(cfg.Routes, cfg.TUN.Name)
				if err != nil {
					log.Printf("Failed to parse client routes: %v", err)
					continue
				}
				
				// Объединяем (клиентские имеют приоритет)
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
			// Ответ keepalive
			keepalive := protocol.CreateKeepaliveMessage()
			mutex.Lock()
			conn.WriteMessage(websocket.BinaryMessage, keepalive.Serialize())
			mutex.Unlock()

		default:
			log.Printf("Unknown message type: 0x%02x", msg.Type)
		}
	}
}

// keepalive периодически отправляет keepalive сообщения
func keepalive(conn *websocket.Conn, cfg *config.ClientConfig, mutex *sync.Mutex) {
	ticker := time.NewTicker(time.Duration(cfg.Timeouts.KeepaliveInterval) * time.Second)
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

		// Парсим CIDR
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
