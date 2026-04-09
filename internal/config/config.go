package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerConfig конфигурация сервера
type ServerConfig struct {
	Server ServerSection `yaml:"server"`
	Auth   AuthSection   `yaml:"auth"`
	TUN    TUNSection    `yaml:"tun"`
	Routes []RouteEntry  `yaml:"routes"`
	Timeouts TimeoutsSection `yaml:"timeouts"`
}

// ClientConfig конфигурация клиента
type ClientConfig struct {
	Client ClientSection `yaml:"client"`
	Auth   ClientAuthSection `yaml:"auth"`
	Proxy  ProxySection  `yaml:"proxy"`
	TUN    ClientTUNSection `yaml:"tun"`
	Routes []RouteEntry  `yaml:"routes"`
	Timeouts ClientTimeoutsSection `yaml:"timeouts"`
}

// ServerSection общие параметры сервера
type ServerSection struct {
	Listen string     `yaml:"listen"`
	Port   int        `yaml:"port"`
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
}

// TUNSection параметры TUN интерфейса сервера
type TUNSection struct {
	Name    string `yaml:"name"`
	IP      string `yaml:"ip"`
	Subnet  int    `yaml:"subnet"`
	IP6     string `yaml:"ip6"`
	Subnet6 int    `yaml:"subnet6"`
}

// ClientSection параметры клиента
type ClientSection struct {
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`
	UseTLS bool   `yaml:"use_tls"`
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
}

// RouteEntry запись маршрута
type RouteEntry struct {
	Dst    string `yaml:"dst"`
	GW     string `yaml:"gw"`
	Metric int    `yaml:"metric"`
}

// TimeoutsSection таймауты сервера
type TimeoutsSection struct {
	KeepaliveInterval int `yaml:"keepalive_interval"`
	KeepaliveTimeout  int `yaml:"keepalive_timeout"`
	FragmentTimeout   int `yaml:"fragment_timeout"`
}

// ClientTimeoutsSection таймауты клиента
type ClientTimeoutsSection struct {
	KeepaliveInterval int `yaml:"keepalive_interval"`
	KeepaliveTimeout  int `yaml:"keepalive_timeout"`
	FragmentTimeout   int `yaml:"fragment_timeout"`
	ReconnectDelay    int `yaml:"reconnect_delay"`
	MaxReconnects     int `yaml:"max_reconnects"`
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
	if c.Timeouts.KeepaliveInterval == 0 {
		c.Timeouts.KeepaliveInterval = 30
	}
	if c.Timeouts.KeepaliveTimeout == 0 {
		c.Timeouts.KeepaliveTimeout = 90
	}
	if c.Timeouts.FragmentTimeout == 0 {
		c.Timeouts.FragmentTimeout = 5
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
	if c.Timeouts.KeepaliveInterval == 0 {
		c.Timeouts.KeepaliveInterval = 30
	}
	if c.Timeouts.KeepaliveTimeout == 0 {
		c.Timeouts.KeepaliveTimeout = 90
	}
	if c.Timeouts.FragmentTimeout == 0 {
		c.Timeouts.FragmentTimeout = 5
	}
	if c.Timeouts.ReconnectDelay == 0 {
		c.Timeouts.ReconnectDelay = 5
	}
	if c.Timeouts.MaxReconnects == 0 {
		c.Timeouts.MaxReconnects = 10
	}
}

// GetAuthTimeout возвращает таймаут аутентификации как Duration
func (c *ServerConfig) GetAuthTimeout() time.Duration {
	return time.Duration(c.Auth.Timeout) * time.Second
}

// GetKeepaliveInterval возвращает интервал keepalive как Duration
func (c *ServerConfig) GetKeepaliveInterval() time.Duration {
	return time.Duration(c.Timeouts.KeepaliveInterval) * time.Second
}

// GetKeepaliveTimeout возвращает таймаут keepalive как Duration
func (c *ServerConfig) GetKeepaliveTimeout() time.Duration {
	return time.Duration(c.Timeouts.KeepaliveTimeout) * time.Second
}

// GetFragmentTimeout возвращает таймаут фрагментации как Duration
func (c *ServerConfig) GetFragmentTimeout() time.Duration {
	return time.Duration(c.Timeouts.FragmentTimeout) * time.Second
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
	return fmt.Sprintf("%s://%s:%d/ws", protocol, c.Client.Server, c.Client.Port)
}
