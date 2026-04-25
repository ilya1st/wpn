package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerConfig конфигурация сервера
type ServerConfig struct {
	Server   ServerSection       `yaml:"server"`
	Auth     AuthSection         `yaml:"auth"`
	TUN      TUNSection          `yaml:"tun"`
	Routes   []RouteEntry        `yaml:"routes"`
	Connection ServerConnectionSettings `yaml:"connection_settings"`
}

// ClientConfig конфигурация клиента
type ClientConfig struct {
	Client     ClientSection        `yaml:"client"`
	Auth       ClientAuthSection    `yaml:"auth"`
	Proxy      ProxySection         `yaml:"proxy"`
	TUN        ClientTUNSection     `yaml:"tun"`
	Routes     []RouteEntry         `yaml:"routes"`
	Connection ClientConnectionSettings `yaml:"connection_settings"`
}

// ServerSection общие параметры сервера
type ServerSection struct {
	Listen string     `yaml:"listen"`
	Port   int        `yaml:"port"`
	Path   string     `yaml:"path"`
	TLS    TLSSection `yaml:"tls"`
}

// TLSSection параметры TLS
type TLSSection struct {
	Enabled bool   `yaml:"enabled"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

// AuthSection параметры аутентификации сервера
type AuthSection struct {
	Timeout int          `yaml:"timeout"`
	Users   []UserEntry  `yaml:"users"`
}

// UserEntry запись пользователя
type UserEntry struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	IP4      string `yaml:"ip4"` // статический IPv4 (пусто = динамический)
	IP6      string `yaml:"ip6"` // статический IPv6 (пусто = динамический)
}

// TUNSection параметры TUN интерфейса сервера
type TUNSection struct {
	Name    string `yaml:"name"`
	IP      string `yaml:"ip"`
	Subnet  int    `yaml:"subnet"`
	IP6     string `yaml:"ip6"`
	Subnet6 int    `yaml:"subnet6"`
	MTU     int    `yaml:"mtu"`
}

// ClientSection параметры клиента
type ClientSection struct {
	Server        string `yaml:"server"`
	Port          int    `yaml:"port"`
	UseTLS        bool   `yaml:"use_tls"`
	WsLocation    string `yaml:"ws_location"`
	AllowInsecure bool   `yaml:"allow_insecure"`
}

// ClientAuthSection аутентификация клиента
type ClientAuthSection struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Timeout  int    `yaml:"timeout"`
}

// ProxySection параметры прокси
type ProxySection struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`
	Address  string `yaml:"address"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ClientTUNSection параметры TUN интерфейса клиента
type ClientTUNSection struct {
	Name   string `yaml:"name"`
	IP     string `yaml:"ip"`
	Subnet int    `yaml:"subnet"`
	MTU    int    `yaml:"mtu"`
}

// RouteEntry запись маршрута
type RouteEntry struct {
	Dst    string `yaml:"dst"`
	GW     string `yaml:"gw"`
	Metric int    `yaml:"metric"`
}

// ServerConnectionSettings настройки соединения сервера
type ServerConnectionSettings struct {
	KeepaliveTimeout       int  `yaml:"keepalive_timeout"`        // таймаут read deadline (сек)
	FragmentTimeout        int  `yaml:"fragment_timeout"`         // таймаут сборки фрагментов (сек)
	Compression            bool `yaml:"compression"`              // сжатие пакетов
	SendPacketBufferSize   int  `yaml:"send_packet_buffer_size"`  // размер буфера записи (кол-во пакетов)
	WriteChannelTimeout    int  `yaml:"write_channel_timeout"`    // таймаут записи в канал (сек)
	ReconnectTimeout       int  `yaml:"reconnect_timeout"`        // время хранения сессии для реконнекта (сек)
}

// ClientConnectionSettings настройки соединения клиента
type ClientConnectionSettings struct {
	KeepaliveInterval int  `yaml:"keepalive_interval"`
	KeepaliveTimeout  int  `yaml:"keepalive_timeout"`
	FragmentTimeout   int  `yaml:"fragment_timeout"`
	ReconnectDelay    int  `yaml:"reconnect_delay"`
	MaxReconnects     int  `yaml:"max_reconnects"`
	Compression       bool `yaml:"compression"`
}

// LoadServerConfig загружает конфигурацию сервера из файла
func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var config ServerConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Установка значений по умолчанию
	config.setDefaults()

	return &config, nil
}

// LoadClientConfig загружает конфигурацию клиента из файла
func LoadClientConfig(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var config ClientConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Установка значений по умолчанию
	config.setDefaults()

	return &config, nil
}

// setDefaults устанавливает значения по умолчанию для сервера
func (c *ServerConfig) setDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8443
	}
	if c.Auth.Timeout == 0 {
		c.Auth.Timeout = 10
	}
	if c.TUN.Name == "" {
		c.TUN.Name = "vpnsrv0"
	}
	if c.TUN.IP == "" {
		c.TUN.IP = "10.0.0.1"
	}
	if c.TUN.Subnet == 0 {
		c.TUN.Subnet = 24
	}
	if c.Connection.KeepaliveTimeout == 0 {
		c.Connection.KeepaliveTimeout = 90
	}
	if c.Connection.FragmentTimeout == 0 {
		c.Connection.FragmentTimeout = 5
	}
	if c.Connection.SendPacketBufferSize == 0 {
		c.Connection.SendPacketBufferSize = 256
	}
	if c.Connection.WriteChannelTimeout == 0 {
		c.Connection.WriteChannelTimeout = 5
	}
	if c.Connection.ReconnectTimeout == 0 {
		c.Connection.ReconnectTimeout = 300
	}
}

// setDefaults устанавливает значения по умолчанию для клиента
func (c *ClientConfig) setDefaults() {
	if c.Client.Port == 0 {
		c.Client.Port = 8443
	}
	if c.Auth.Timeout == 0 {
		c.Auth.Timeout = 10
	}
	if c.TUN.Name == "" {
		c.TUN.Name = "vpnclient0"
	}
	if c.Connection.KeepaliveInterval == 0 {
		c.Connection.KeepaliveInterval = 30
	}
	if c.Connection.KeepaliveTimeout == 0 {
		c.Connection.KeepaliveTimeout = 90
	}
	if c.Connection.FragmentTimeout == 0 {
		c.Connection.FragmentTimeout = 5
	}
	if c.Connection.ReconnectDelay == 0 {
		c.Connection.ReconnectDelay = 5
	}
	if c.Connection.MaxReconnects == 0 {
		c.Connection.MaxReconnects = 10
	}
}

// GetAuthTimeout возвращает таймаут аутентификации как Duration
func (c *ServerConfig) GetAuthTimeout() time.Duration {
	return time.Duration(c.Auth.Timeout) * time.Second
}

// GetKeepaliveTimeout возвращает таймаут keepalive как Duration
func (c *ServerConfig) GetKeepaliveTimeout() time.Duration {
	return time.Duration(c.Connection.KeepaliveTimeout) * time.Second
}

// GetFragmentTimeout возвращает таймаут фрагментации как Duration
func (c *ServerConfig) GetFragmentTimeout() time.Duration {
	return time.Duration(c.Connection.FragmentTimeout) * time.Second
}

// GetSendPacketBufferSize возвращает размер буфера записи
func (c *ServerConfig) GetSendPacketBufferSize() int {
	return c.Connection.SendPacketBufferSize
}

// GetWriteChannelTimeout возвращает таймаут записи в канал как Duration
func (c *ServerConfig) GetWriteChannelTimeout() time.Duration {
	return time.Duration(c.Connection.WriteChannelTimeout) * time.Second
}

// GetReconnectTimeout возвращает время хранения сессии для реконнекта как Duration
func (c *ServerConfig) GetReconnectTimeout() time.Duration {
	return time.Duration(c.Connection.ReconnectTimeout) * time.Second
}

// GetFragmentTimeout возвращает таймаут фрагментации как Duration
func (c *ClientConfig) GetFragmentTimeout() time.Duration {
	return time.Duration(c.Connection.FragmentTimeout) * time.Second
}

// GetAuthTimeout возвращает таймаут аутентификации клиента как Duration
func (c *ClientConfig) GetAuthTimeout() time.Duration {
	return time.Duration(c.Auth.Timeout) * time.Second
}

// GetServerURL возвращает URL сервера для подключения
func (c *ClientConfig) GetServerURL() string {
	protocol := "ws"
	if c.Client.UseTLS {
		protocol = "wss"
	}
	location := "/ws"
	if c.Client.WsLocation != "" {
		location = c.Client.WsLocation
	}
	return fmt.Sprintf("%s://%s:%d%s", protocol, c.Client.Server, c.Client.Port, location)
}

// CompressionEnabled возвращает true если сжатие включено
func (c *ServerConfig) CompressionEnabled() bool {
	return c.Connection.Compression
}

// CompressionEnabled возвращает true если сжатие включено
func (c *ClientConfig) CompressionEnabled() bool {
	return c.Connection.Compression
}
