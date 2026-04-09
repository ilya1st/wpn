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
	})
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()

	// Настройка TUN интерфейса
	if err := tunIface.Setup(); err != nil {
		log.Fatalf("Failed to setup TUN interface: %v", err)
	}

	log.Printf("TUN interface %s created", tunIface.Name())

	// Настройка маршрутов сервера
	// Используем реальное имя интерфейса (на macOS это utun0/1/... а не из конфига)
	routeManager := routes.NewManager(tunIface.Name())

	// Автоматически добавляем маршрут на подсеть сервера через TUN интерфейс
	if cfg.TUN.IP != "" && cfg.TUN.Subnet > 0 {
		subnet := fmt.Sprintf("%s/%d", cfg.TUN.IP, cfg.TUN.Subnet)
		_, dst, err := net.ParseCIDR(subnet)
		if err != nil {
			log.Fatalf("Failed to parse subnet %s: %v", subnet, err)
		}
		log.Printf("Adding local subnet route: %s via %s", subnet, tunIface.Name())
		routeManager.AddRoute(routes.Route{
			Dst:    dst,
			Metric: 0,
		})
	}

	if len(cfg.Routes) > 0 {
		serverRoutes, err := routes.ParseRoutesFromConfig(cfg.Routes, cfg.TUN.Name)
		if err != nil {
			log.Fatalf("Failed to parse server routes: %v", err)
		}
		routeManager.AddRoutes(serverRoutes)
	}

	// Применяем все маршруты (автодобавленные + из конфига)
	if err := routeManager.ApplyRoutes(); err != nil {
		log.Printf("Warning: failed to apply server routes: %v", err)
	} else {
		log.Printf("Server routes applied")
	}

	// Создание WebSocket сервера
	wsServer := ws.NewServer(ws.ServerConfig{
		Listen: cfg.Server.Listen,
		Port:   cfg.Server.Port,
		Path:   cfg.Server.Path,
		TLS:    cfg.Server.TLS.Enabled,
		Cert:   cfg.Server.TLS.Cert,
		Key:    cfg.Server.TLS.Key,
	})

	// Запуск сервера с обработчиком подключений
	errCh := make(chan error, 1)
	go func() {
		err := wsServer.Start(func(conn *websocket.Conn) {
			handleClient(conn, tunIface, routeManager, cfg)
		})
		if err != nil {
			errCh <- err
		}
	}()

	// Ожидание сигнала
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
}

// handleClient обрабатывает подключение клиента
func handleClient(conn *websocket.Conn, tunIface *tun.Interface, routeManager *routes.Manager, cfg *config.ServerConfig) {
	clientIP := conn.RemoteAddr().String()
	log.Printf("Client connected: %s", clientIP)

	// Отправка AUTH_CHALLENGE
	authChallenge := protocol.CreateControlMessage(protocol.ControlTypeAuthChallenge, []byte{})
	if err := conn.WriteMessage(websocket.BinaryMessage, authChallenge.Serialize()); err != nil {
		log.Printf("Failed to send auth challenge to %s: %v", clientIP, err)
		return
	}

	// Ожидание AUTH_RESPONSE
	conn.SetReadDeadline(time.Now().Add(cfg.GetAuthTimeout()))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		log.Printf("Failed to read auth response from %s: %v", clientIP, err)
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

	// Парсинг учётных данных
	payload, err := msg.GetControlPayload()
	if err != nil {
		log.Printf("Failed to get control payload: %v", err)
		return
	}
	log.Printf("Auth payload received: %d bytes: %v", len(payload), payload)
	username, password, err := protocol.ParseAuthResponsePayload(payload)
	if err != nil {
		log.Printf("Failed to parse auth payload: %v, raw: %v", err, payload)
		return
	}
	log.Printf("Parsed credentials: username=%s, password=%s", username, password)

	// Проверка авторизации
	if !authenticate(username, password, cfg) {
		log.Printf("Authentication failed for user %s from %s", username, clientIP)
		failureMsg := protocol.CreateControlMessage(protocol.ControlTypeAuthFailure, 
			[]byte("Invalid credentials"))
		conn.WriteMessage(websocket.BinaryMessage, failureMsg.Serialize())
		return
	}

	log.Printf("Client %s authenticated as %s", clientIP, username)

	// Отправка AUTH_SUCCESS
	authSuccess := protocol.CreateControlMessage(protocol.ControlTypeAuthSuccess,
		protocol.CreateAuthSuccessPayload([]byte{10, 0, 0, 2}, []byte{10, 0, 0, 1}, 24))
	if err := conn.WriteMessage(websocket.BinaryMessage, authSuccess.Serialize()); err != nil {
		log.Printf("Failed to send auth success to %s: %v", clientIP, err)
		return
	}

	// Отправка конфигурации маршрутов
	if len(cfg.Routes) > 0 {
		if err := sendRoutesConfig(conn, cfg.Routes); err != nil {
			log.Printf("Failed to send routes config to %s: %v", clientIP, err)
			return
		}
	}

	// Мьютекс для синхронизации записей в WebSocket
	var wsMutex sync.Mutex

	// Запуск цикла обмена данными
	go tunToWS(tunIface, conn, &wsMutex)
	go keepaliveMonitor(conn, cfg, &wsMutex)
	wsToTUN(tunIface, conn, cfg, &wsMutex)
}

// authenticate проверяет учётные данные
func authenticate(username, password string, cfg *config.ServerConfig) bool {
	for _, user := range cfg.Auth.Users {
		if user.Username == username && user.Password == password {
			return true
		}
	}
	return false
}

// sendRoutesConfig отправляет конфигурацию маршрутов клиенту
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
		// Reserved byte is 0

		payload = append(payload, routeData...)
	}

	routesMsg := protocol.CreateControlMessage(protocol.ControlTypeRoutesConfig, payload)
	return conn.WriteMessage(websocket.BinaryMessage, routesMsg.Serialize())
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
func wsToTUN(tunIface *tun.Interface, conn *websocket.Conn, cfg *config.ServerConfig, mutex *sync.Mutex) {
	// Устанавливаем начальный deadline
	conn.SetReadDeadline(time.Now().Add(cfg.GetKeepaliveTimeout()))
	
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read from WebSocket: %v", err)
			return
		}

		// Обновляем deadline после получения любого сообщения
		conn.SetReadDeadline(time.Now().Add(cfg.GetKeepaliveTimeout()))

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

// keepaliveMonitor контролирует соединение и отправляет keepalive
func keepaliveMonitor(conn *websocket.Conn, cfg *config.ServerConfig, mutex *sync.Mutex) {
	ticker := time.NewTicker(cfg.GetKeepaliveInterval())
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
