package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ilya1st/wpn/internal/compression"
	"github.com/ilya1st/wpn/internal/config"
	"github.com/ilya1st/wpn/internal/fragment"
	"github.com/ilya1st/wpn/internal/protocol"
	"github.com/ilya1st/wpn/internal/routes"
	"github.com/ilya1st/wpn/internal/session"
	"github.com/ilya1st/wpn/internal/tun"
	wstransport "github.com/ilya1st/wpn/internal/ws"
)

func main() {
	configFile := flag.String("config", "server.yaml", "Path to server config file")
	flag.Parse()

	// Загрузка конфигурации
	cfg, err := config.LoadServerConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Создание TUN интерфейса
	tunIface, err := tun.New(tun.Config{
		Name:    cfg.TUN.Name,
		IP:      cfg.TUN.IP,
		Subnet:  cfg.TUN.Subnet,
		IP6:     cfg.TUN.IP6,
		Subnet6: cfg.TUN.Subnet6,
		MTU:     cfg.TUN.MTU,
	})
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()

	if err := tunIface.Setup(); err != nil {
		log.Fatalf("Failed to setup TUN interface: %v", err)
	}

	log.Printf("TUN interface %s created", tunIface.Name())

	// Маршруты сервера
	routeManager := routes.NewManager(tunIface.Name())

	if cfg.TUN.IP != "" && cfg.TUN.Subnet > 0 {
		subnet := fmt.Sprintf("%s/%d", cfg.TUN.IP, cfg.TUN.Subnet)
		_, dst, err := net.ParseCIDR(subnet)
		if err != nil {
			log.Fatalf("Failed to parse subnet %s: %v", subnet, err)
		}
		log.Printf("Adding local subnet route: %s via %s", subnet, tunIface.Name())
		routeManager.AddRoute(routes.Route{Dst: dst, Metric: 0})
	}

	if len(cfg.Routes) > 0 {
		serverRoutes, err := routes.ParseRoutesFromConfig(cfg.Routes, cfg.TUN.Name)
		if err != nil {
			log.Fatalf("Failed to parse server routes: %v", err)
		}
		routeManager.AddRoutes(serverRoutes)
	}

	if err := routeManager.ApplyRoutes(); err != nil {
		log.Printf("Warning: failed to apply server routes: %v", err)
	} else {
		log.Printf("Server routes applied")
	}

	// Реестр сессий и пулы адресов
	registry := session.NewRegistry()

	if cfg.TUN.IP != "" && cfg.TUN.Subnet > 0 {
		_, ip4Net, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.TUN.IP, cfg.TUN.Subnet))
		if err != nil {
			log.Fatalf("Failed to parse IPv4 subnet: %v", err)
		}
		registry.SetIPv4Pool(session.NewIPPool(ip4Net))
		log.Printf("IPv4 pool created: %s", ip4Net)
	}

	if cfg.TUN.IP6 != "" && cfg.TUN.Subnet6 > 0 {
		_, ip6Net, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.TUN.IP6, cfg.TUN.Subnet6))
		if err != nil {
			log.Fatalf("Failed to parse IPv6 subnet: %v", err)
		}
		registry.SetIPv6Pool(session.NewIPPool(ip6Net))
		log.Printf("IPv6 pool created: %s", ip6Net)
	}

	// WebSocket сервер
	wsServer := wstransport.NewServer(wstransport.ServerConfig{
		Listen: cfg.Server.Listen,
		Port:   cfg.Server.Port,
		Path:   cfg.Server.Path,
		TLS:    cfg.Server.TLS.Enabled,
		Cert:   cfg.Server.TLS.Cert,
		Key:    cfg.Server.TLS.Key,
	})

	errCh := make(chan error, 1)
	go func() {
		err := wsServer.Start(func(conn *websocket.Conn) {
			handleClient(conn, tunIface, routeManager, registry, cfg)
		})
		if err != nil {
			errCh <- err
		}
	}()

	// Один TUN reader — читает пакеты и маршрутизирует по сессиям
	go tunRouter(tunIface, registry, cfg)

	// Мониторинг мёртвых сессий
	go sessionCleanup(registry, cfg)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("VPN server started. Waiting for connections...")

	select {
	case <-sigCh:
		log.Println("Received shutdown signal")
	case err := <-errCh:
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Shutting down...")
	for _, s := range registry.ActiveSessions() {
		s.StopWriter()
		s.Lock()
		if s.WSConn != nil {
			s.WSConn.Close()
		}
		s.Unlock()
	}
}

// handleClient обрабатывает подключение одного клиента
func handleClient(conn *websocket.Conn, tunIface *tun.Interface, _ *routes.Manager, registry *session.Registry, cfg *config.ServerConfig) {
	clientAddr := conn.RemoteAddr().String()
	log.Printf("Client connected: %s", clientAddr)

	// AUTH_CHALLENGE
	authChallenge := protocol.CreateControlMessage(protocol.ControlTypeAuthChallenge, []byte{})
	if err := conn.WriteMessage(websocket.BinaryMessage, authChallenge.Serialize()); err != nil {
		log.Printf("Failed to send auth challenge to %s: %v", clientAddr, err)
		return
	}

	// Ожидание AUTH_RESPONSE
	conn.SetReadDeadline(time.Now().Add(cfg.GetAuthTimeout()))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		log.Printf("Failed to read auth response from %s: %v", clientAddr, err)
		return
	}
	if msgType != websocket.BinaryMessage {
		log.Printf("Expected binary message, got: %d", msgType)
		return
	}

	msg, err := protocol.DeserializeMessage(data)
	if err != nil {
		log.Printf("Failed to deserialize message: %v", err)
		return
	}
	if msg.Type != protocol.MessageTypeControl {
		log.Printf("Expected control message, got: 0x%02x", msg.Type)
		return
	}

	controlType, err := msg.GetControlType()
	if err != nil {
		log.Printf("Failed to get control type: %v", err)
		return
	}
	if controlType != protocol.ControlTypeAuthResponse {
		log.Printf("Expected auth response, got: 0x%02x", controlType)
		return
	}

	payload, err := msg.GetControlPayload()
	if err != nil {
		log.Printf("Failed to get control payload: %v", err)
		return
	}
	username, password, err := protocol.ParseAuthResponsePayload(payload)
	if err != nil {
		log.Printf("Failed to parse auth payload: %v", err)
		return
	}

	// Проверка авторизации
	userEntry := findUser(username, password, cfg)
	if userEntry == nil {
		log.Printf("Authentication failed for user %s from %s", username, clientAddr)
		failureMsg := protocol.CreateControlMessage(protocol.ControlTypeAuthFailure, []byte("Invalid credentials"))
		conn.WriteMessage(websocket.BinaryMessage, failureMsg.Serialize())
		return
	}

	log.Printf("Client %s authenticated as %s", clientAddr, username)

	// Статические IP из конфига
	var staticIP4, staticIP6 net.IP
	if userEntry.IP4 != "" {
		staticIP4 = net.ParseIP(userEntry.IP4)
	}
	if userEntry.IP6 != "" {
		staticIP6 = net.ParseIP(userEntry.IP6)
	}

	// Создаём сессию
	sess, err := registry.CreateSession(username, clientAddr, conn, staticIP4, staticIP6)
	if err != nil {
		log.Printf("Failed to create session for %s: %v", username, err)
		return
	}

	log.Printf("Session %s created: IP4=%v, IP6=%v", sess.ID, sess.IP4, sess.IP6)

	// AUTH_SUCCESS
	serverIP4 := net.ParseIP(cfg.TUN.IP)
	serverIP6 := net.ParseIP(cfg.TUN.IP6)

	authSuccessPayload := protocol.CreateAuthSuccessPayload(
		sess.ID,
		ipBytesOrNil(sess.IP4),
		ipBytesOrNil(sess.IP6),
		ipBytesOrNil(serverIP4),
		ipBytesOrNil(serverIP6),
		byte(cfg.TUN.Subnet),
		byte(cfg.TUN.Subnet6),
	)
	authSuccess := protocol.CreateControlMessage(protocol.ControlTypeAuthSuccess, authSuccessPayload)
	if err := conn.WriteMessage(websocket.BinaryMessage, authSuccess.Serialize()); err != nil {
		log.Printf("Failed to send auth success: %v", err)
		registry.RemoveSession(sess.ID)
		return
	}

	// ROUTES_CONFIG
	if len(cfg.Routes) > 0 {
		if err := sendRoutesConfig(conn, cfg.Routes); err != nil {
			log.Printf("Failed to send routes: %v", err)
			registry.RemoveSession(sess.ID)
			return
		}
	}

	// Инициализация writer'а сессии (канал + goroutine)
	sess.InitWriter(256)
	sess.StartWriter(cfg.GetKeepaliveInterval(), func(err error) {
		log.Printf("[%s] Writer error: %v", sess.ID, err)
	})

	// Чтение из WebSocket клиента → TUN
	assembler := fragment.NewAssembler(cfg.GetFragmentTimeout(), func(fragmentID uint32) {
		log.Printf("[%s] Fragment assembly timeout for ID=%d", sess.ID, fragmentID)
	})
	defer assembler.Cleanup()

	wsToTUNForSession(tunIface, sess, cfg, assembler, registry)

	log.Printf("Session %s ended", sess.ID)
	registry.RemoveSession(sess.ID)
}

func findUser(username, password string, cfg *config.ServerConfig) *config.UserEntry {
	for i := range cfg.Auth.Users {
		u := &cfg.Auth.Users[i]
		if u.Username == username && u.Password == password {
			return u
		}
	}
	return nil
}

func ipBytesOrNil(ip net.IP) []byte {
	if ip == nil {
		return []byte{}
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip.To16()
}

func sendRoutesConfig(conn *websocket.Conn, routeEntries []config.RouteEntry) error {
	payload := make([]byte, 1)
	payload[0] = byte(len(routeEntries))

	for _, entry := range routeEntries {
		dstBytes := []byte(entry.Dst)
		gwBytes := []byte(entry.GW)
		routeData := make([]byte, 1+len(dstBytes)+1+len(gwBytes)+2)
		routeData[0] = byte(len(dstBytes))
		copy(routeData[1:1+len(dstBytes)], dstBytes)
		routeData[1+len(dstBytes)] = byte(len(gwBytes))
		copy(routeData[2+len(dstBytes):2+len(dstBytes)+len(gwBytes)], gwBytes)
		routeData[2+len(dstBytes)+len(gwBytes)] = byte(entry.Metric)
		payload = append(payload, routeData...)
	}

	routesMsg := protocol.CreateControlMessage(protocol.ControlTypeRoutesConfig, payload)
	return conn.WriteMessage(websocket.BinaryMessage, routesMsg.Serialize())
}

// tunRouter — единственный reader TUN интерфейса.
// Читает пакеты, определяет целевой IP, маршрутизирует через QueueWrite.
func tunRouter(tunIface *tun.Interface, registry *session.Registry, cfg *config.ServerConfig) {
	buf := make([]byte, 65535)

	for {
		n, err := tunIface.Read(buf)
		if err != nil {
			log.Printf("tunRouter: Failed to read from TUN: %v", err)
			return
		}

		packet := make([]byte, n)
		copy(packet, buf[:n])
		isIPv6 := tun.IsIPv6Packet(packet)
		targetIP := getPacketDstIP(packet)
		targetSession := registry.GetSessionByIP(targetIP)

		if targetSession == nil {
			continue
		}

		msg := buildSessionMessage(packet, isIPv6, cfg)
		if !targetSession.QueueWrite(msg) {
			log.Printf("[%s] writeCh full, dropping packet (dst=%v)", targetSession.ID, targetIP)
		}
	}
}

// buildSessionMessage создаёт байты сообщения для отправки клиенту
func buildSessionMessage(packet []byte, isIPv6 bool, cfg *config.ServerConfig) []byte {
	if cfg.CompressionEnabled() {
		compressed, err := compression.Compress(packet)
		if err == nil && len(compressed) < len(packet) {
			msg := protocol.CreateDataMessage(compressed, isIPv6)
			msg.Flags |= protocol.FlagCompressed
			return msg.Serialize()
		}
	}

	msg := protocol.CreateDataMessage(packet, isIPv6)
	return msg.Serialize()
}

// wsToTUNForSession читает из сессии и пишет в TUN
func wsToTUNForSession(tunIface *tun.Interface, sess *session.Session, cfg *config.ServerConfig, assembler *fragment.Assembler, _ *session.Registry) {
	sess.RLock()
	conn := sess.WSConn
	sess.RUnlock()

	if conn == nil {
		return
	}

	conn.SetReadDeadline(time.Now().Add(cfg.GetKeepaliveTimeout()))

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[%s] Failed to read from WebSocket: %v", sess.ID, err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(cfg.GetKeepaliveTimeout()))
		sess.UpdateActivity()

		if msgType != websocket.BinaryMessage {
			log.Printf("[%s] Expected binary, got: %d", sess.ID, msgType)
			continue
		}

		msg, err := protocol.DeserializeMessage(data)
		if err != nil {
			log.Printf("[%s] Failed to deserialize: %v", sess.ID, err)
			continue
		}

		// Статистика
		sess.Lock()
		sess.PacketsRecv++
		sess.BytesRecv += uint64(len(data))
		sess.Unlock()

		switch msg.Type {
		case protocol.MessageTypeData:
			payload := msg.Payload
			if msg.Flags&protocol.FlagCompressed != 0 {
				decompressed, err := compression.Decompress(payload)
				if err != nil {
					log.Printf("[%s] Failed to decompress: %v", sess.ID, err)
					continue
				}
				payload = decompressed
			}
			if _, err := tunIface.Write(payload); err != nil {
				log.Printf("[%s] Failed to write to TUN: %v", sess.ID, err)
			}

		case protocol.MessageTypeFragment:
			packet, isIPv6, complete := assembler.HandleFragment(msg)
			if complete {
				payload := packet
				if msg.Flags&protocol.FlagCompressed != 0 {
					decompressed, err := compression.Decompress(payload)
					if err != nil {
						log.Printf("[%s] Failed to decompress assembled: %v", sess.ID, err)
						continue
					}
					payload = decompressed
				}
				if _, err := tunIface.Write(payload); err != nil {
					log.Printf("[%s] Failed to write assembled: %v", sess.ID, err)
				} else {
					log.Printf("[%s] Assembled: %d bytes (IPv6=%v)", sess.ID, len(payload), isIPv6)
				}
			}

		case protocol.MessageTypeKeepalive:
			// Игнорируем
		}
	}
}

// sessionCleanup удаляет мёртвые сессии
func sessionCleanup(registry *session.Registry, cfg *config.ServerConfig) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for _, s := range registry.ActiveSessions() {
			s.RLock()
			lastActivity := s.LastActivity
			s.RUnlock()

			if time.Since(lastActivity) > cfg.GetKeepaliveTimeout() {
				log.Printf("[%s] Session expired (last activity: %s ago)",
					s.ID, time.Since(lastActivity).Round(time.Second))
				s.StopWriter()
				s.Lock()
				s.State = session.SessionExpired
				if s.WSConn != nil {
					s.WSConn.Close()
				}
				s.Unlock()
			}
		}
		registry.CleanupExpired()
	}
}

// getPacketDstIP извлекает IP назначения из пакета
func getPacketDstIP(packet []byte) net.IP {
	if len(packet) == 0 {
		return nil
	}
	version := packet[0] >> 4
	switch version {
	case 4:
		if len(packet) < 20 {
			return nil
		}
		return net.IPv4(packet[16], packet[17], packet[18], packet[19])
	case 6:
		if len(packet) < 40 {
			return nil
		}
		return net.IP(packet[24:40])
	}
	return nil
}
