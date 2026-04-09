package tun

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

// Config конфигурация TUN интерфейса
type Config struct {
	Name    string
	IP      string
	Subnet  int
	IP6     string
	Subnet6 int
	MTU     int
}

// Interface представляет TUN интерфейс
type Interface struct {
	fd     *os.File
	config Config
}

const (
	tunDevice  = "/dev/net/tun"
	ifNameSize = 16
	IFF_TUN    = 0x0001
	IFF_NO_PI  = 0x1000
)

// ifreq структура для ioctl TUNSETIFF
type ifreq struct {
	Name  [ifNameSize]byte
	Flags uint16
	_     [28]byte // padding до размера IFNAMSIZ + sizeof(short) + padding
}

// New создаёт новый TUN интерфейс
func New(cfg Config) (*Interface, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("unsupported OS: %s (gvisor TUN only supports Linux)", runtime.GOOS)
	}

	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}

	// Открываем TUN устройство
	fd, err := syscall.Open(tunDevice, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tunDevice, err)
	}

	// Подготавливаем структуру ifreq
	var ifr ifreq
	ifr.Flags = IFF_TUN | IFF_NO_PI

	// Копируем имя интерфейса
	nameBytes := []byte(cfg.Name)
	if len(nameBytes) >= ifNameSize {
		nameBytes = nameBytes[:ifNameSize-1]
	}
	copy(ifr.Name[:], nameBytes)

	// Вызываем ioctl TUNSETIFF
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TUNSETIFF),
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Получаем реальное имя интерфейса (если cfg.Name было пустым)
	actualName := string(ifr.Name[:])
	// Убираем нулевые байты
	for i, b := range actualName {
		if b == 0 {
			actualName = actualName[:i]
			break
		}
	}

	// Оборачиваем fd в os.File для удобного Read/Write
	file := os.NewFile(uintptr(fd), actualName)
	if file == nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("failed to create file from fd")
	}

	tun := &Interface{
		fd:     file,
		config: cfg,
	}

	// Обновляем имя в конфиге (если оно было пустым)
	if tun.config.Name == "" {
		tun.config.Name = actualName
	}

	return tun, nil
}

// Setup настраивает TUN интерфейс (требует root прав)
func (t *Interface) Setup() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	// Настройка IPv4
	if t.config.IP != "" && t.config.Subnet > 0 {
		addr := fmt.Sprintf("%s/%d", t.config.IP, t.config.Subnet)
		if err := t.configureAddress(addr); err != nil {
			return fmt.Errorf("set IPv4 address: %w", err)
		}
	}

	// Настройка IPv6
	if t.config.IP6 != "" && t.config.Subnet6 > 0 {
		addr := fmt.Sprintf("%s/%d", t.config.IP6, t.config.Subnet6)
		if err := t.configureAddress6(addr); err != nil {
			return fmt.Errorf("set IPv6 address: %w", err)
		}
	}

	// Установка MTU
	if t.config.MTU > 0 {
		if err := t.setMTU(t.config.MTU); err != nil {
			return fmt.Errorf("set MTU: %w", err)
		}
	}

	// Активация интерфейса
	if err := t.setUp(); err != nil {
		return fmt.Errorf("bring up interface: %w", err)
	}

	return nil
}

// configureAddress устанавливает IPv4 адрес
func (t *Interface) configureAddress(addr string) error {
	cmd := exec.Command("ip", "addr", "add", addr, "dev", t.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr add: %s: %w", string(output), err)
	}
	return nil
}

// configureAddress6 устанавливает IPv6 адрес
func (t *Interface) configureAddress6(addr string) error {
	cmd := exec.Command("ip", "-6", "addr", "add", addr, "dev", t.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip -6 addr add: %s: %w", string(output), err)
	}
	return nil
}

// setMTU устанавливает MTU интерфейса
func (t *Interface) setMTU(mtu int) error {
	cmd := exec.Command("ip", "link", "set", "dev", t.Name(), "mtu", fmt.Sprintf("%d", mtu))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set mtu: %s: %w", string(output), err)
	}
	return nil
}

// setUp активирует интерфейс
func (t *Interface) setUp() error {
	cmd := exec.Command("ip", "link", "set", "dev", t.Name(), "up")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set up: %s: %w", string(output), err)
	}
	return nil
}

// Read читает пакет из TUN
func (t *Interface) Read(buf []byte) (int, error) {
	return t.fd.Read(buf)
}

// Write записывает пакет в TUN
func (t *Interface) Write(buf []byte) (int, error) {
	return t.fd.Write(buf)
}

// Close закрывает TUN интерфейс
func (t *Interface) Close() error {
	return t.fd.Close()
}

// Name возвращает имя интерфейса
func (t *Interface) Name() string {
	if t.config.Name != "" {
		return t.config.Name
	}
	return t.fd.Name()
}

// AddRoute добавляет маршрут через TUN интерфейс
func AddRoute(dst, gw, dev string, metric int) error {
	args := []string{"route", "add", dst}
	if gw != "" {
		args = append(args, "via", gw)
	}
	args = append(args, "dev", dev)
	if metric > 0 {
		args = append(args, "metric", fmt.Sprintf("%d", metric))
	}

	cmd := exec.Command("ip", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip route add: %s: %w", string(output), err)
	}
	return nil
}

// AddRoute6 добавляет IPv6 маршрут
func AddRoute6(dst, gw, dev string, metric int) error {
	args := []string{"-6", "route", "add", dst}
	if gw != "" {
		args = append(args, "via", gw)
	}
	args = append(args, "dev", dev)
	if metric > 0 {
		args = append(args, "metric", fmt.Sprintf("%d", metric))
	}

	cmd := exec.Command("ip", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip -6 route add: %s: %w", string(output), err)
	}
	return nil
}

// DeleteRoute удаляет маршрут
func DeleteRoute(dst, dev string) error {
	cmd := exec.Command("ip", "route", "del", dst, "dev", dev)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip route del: %s: %w", string(output), err)
	}
	return nil
}

// DeleteRoute6 удаляет IPv6 маршрут
func DeleteRoute6(dst, dev string) error {
	cmd := exec.Command("ip", "-6", "route", "del", dst, "dev", dev)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip -6 route del: %s: %w", string(output), err)
	}
	return nil
}

// GetInterfaceIP возвращает IP адрес интерфейса
func GetInterfaceIP(name string) (net.IP, error) {
	ifce, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("get interface: %w", err)
	}

	addrs, err := ifce.Addrs()
	if err != nil {
		return nil, fmt.Errorf("get addresses: %w", err)
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.To4() != nil {
				return ipnet.IP, nil
			}
		}
	}

	return nil, fmt.Errorf("no IPv4 address found for interface %s", name)
}

// IsIPv6Packet проверяет является ли пакет IPv6 по первому байту
func IsIPv6Packet(packet []byte) bool {
	if len(packet) == 0 {
		return false
	}
	// IPv4 начинается с 0x4x, IPv6 с 0x6x
	version := packet[0] >> 4
	return version == 6
}
