package ws

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

// ServerConfig конфигурация WebSocket сервера
type ServerConfig struct {
	Listen string
	Port   int
	TLS    bool
	Cert   string
	Key    string
}

// ClientConfig конфигурация WebSocket клиента
type ClientConfig struct {
	ServerURL string
	Proxy     *ProxyConfig
	TLS       bool
}

// ProxyConfig конфигурация прокси
type ProxyConfig struct {
	Enabled  bool
	Type     string // "http" или "socks"
	Address  string
	Port     int
	Username string
	Password string
}

// Server WebSocket сервер
type Server struct {
	config  ServerConfig
	upgrader websocket.Upgrader
}

// NewServer создаёт новый WebSocket сервер
func NewServer(config ServerConfig) *Server {
	return &Server{
		config: config,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  65536,
			WriteBufferSize: 65536,
			CheckOrigin: func(r *http.Request) bool {
				return true // Разрешаем все подключения (авторизация позже)
			},
		},
	}
}

// Start запускает сервер
func (s *Server) Start(handler func(conn *websocket.Conn)) error {
	addr := fmt.Sprintf("%s:%d", s.config.Listen, s.config.Port)

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("WebSocket upgrade error: %v\n", err)
			return
		}
		defer conn.Close()

		handler(conn)
	})

	var err error
	if s.config.TLS {
		fmt.Printf("Starting WebSocket server with TLS on %s\n", addr)
		err = http.ListenAndServeTLS(addr, s.config.Cert, s.config.Key, nil)
	} else {
		fmt.Printf("Starting WebSocket server without TLS on %s\n", addr)
		err = http.ListenAndServe(addr, nil)
	}

	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	return nil
}

// Client WebSocket клиент
type Client struct {
	config ClientConfig
	conn   *websocket.Conn
}

// NewClient создаёт новый WebSocket клиент
func NewClient(config ClientConfig) *Client {
	return &Client{
		config: config,
	}
}

// Connect подключается к серверу
func (c *Client) Connect() error {
	u, err := url.Parse(c.config.ServerURL)
	if err != nil {
		return fmt.Errorf("parse server URL: %w", err)
	}

	dialer := websocket.Dialer{
		ReadBufferSize:  65536,
		WriteBufferSize: 65536,
		HandshakeTimeout: 10 * time.Second,
	}

	// Настройка прокси если включен
	if c.config.Proxy != nil && c.config.Proxy.Enabled {
		if err := c.configureProxy(&dialer); err != nil {
			return fmt.Errorf("configure proxy: %w", err)
		}
	}

	// Настройка TLS
	if c.config.TLS {
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: false,
		}
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial server: %w", err)
	}

	c.conn = conn
	return nil
}

// configureProxy настраивает прокси для диалера
func (c *Client) configureProxy(dialer *websocket.Dialer) error {
	proxyConfig := c.config.Proxy

	var proxyURL *url.URL

	switch proxyConfig.Type {
	case "http":
		// HTTP CONNECT прокси
		scheme := "http"
		if c.config.TLS {
			scheme = "https"
		}
		
		proxyURL = &url.URL{
			Scheme: scheme,
			Host:   fmt.Sprintf("%s:%d", proxyConfig.Address, proxyConfig.Port),
		}

		if proxyConfig.Username != "" && proxyConfig.Password != "" {
			proxyURL.User = url.UserPassword(proxyConfig.Username, proxyConfig.Password)
		}

		// Используем http.Transport с прокси
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
		dialer.NetDial = func(network, addr string) (net.Conn, error) {
			return transport.Dial(network, addr)
		}
		dialer.Proxy = http.ProxyURL(proxyURL)

	case "socks":
		// SOCKS5 прокси
		auth := &proxy.Auth{}
		if proxyConfig.Username != "" {
			auth.User = proxyConfig.Username
			auth.Password = proxyConfig.Password
		}

		socksAddr := fmt.Sprintf("%s:%d", proxyConfig.Address, proxyConfig.Port)
		dial, err := proxy.SOCKS5("tcp", socksAddr, auth, proxy.Direct)
		if err != nil {
			return fmt.Errorf("create SOCKS5 dialer: %w", err)
		}

		dialer.NetDial = func(network, addr string) (net.Conn, error) {
			return dial.Dial(network, addr)
		}

	default:
		return fmt.Errorf("unsupported proxy type: %s", proxyConfig.Type)
	}

	return nil
}

// ReadMessage читает сообщение
func (c *Client) ReadMessage() (int, []byte, error) {
	return c.conn.ReadMessage()
}

// WriteMessage записывает сообщение
func (c *Client) WriteMessage(messageType int, data []byte) error {
	return c.conn.WriteMessage(messageType, data)
}

// Close закрывает соединение
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Connection возвращает underlying соединение
func (c *Client) Connection() *websocket.Conn {
	return c.conn
}
