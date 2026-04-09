//go:build linux

package tun

import (
	"fmt"
	"syscall"
	"unsafe"
)

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

// newTUN создаёт новый TUN интерфейс (Linux)
func newTUN(name string) (fileInterface, string, error) {
	// Открываем TUN устройство
	fd, err := syscall.Open(tunDevice, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", tunDevice, err)
	}

	// Подготавливаем структуру ifreq
	var ifr ifreq
	ifr.Flags = IFF_TUN | IFF_NO_PI

	// Копируем имя интерфейса
	nameBytes := []byte(name)
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
		return nil, "", fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Получаем реальное имя интерфейса (если name было пустым)
	actualName := string(ifr.Name[:])
	// Убираем нулевые байты
	for i, b := range actualName {
		if b == 0 {
			actualName = actualName[:i]
			break
		}
	}

	// Оборачиваем fd в интерфейс для Read/Write
	file := &linuxFile{fd: fd, name: actualName}
	return file, actualName, nil
}

// linuxFile реализация fileInterface для Linux
type linuxFile struct {
	fd   int
	name string
}

func (f *linuxFile) Read(b []byte) (int, error) {
	return syscall.Read(f.fd, b)
}

func (f *linuxFile) Write(b []byte) (int, error) {
	return syscall.Write(f.fd, b)
}

func (f *linuxFile) Close() error {
	return syscall.Close(f.fd)
}

func (f *linuxFile) Name() string {
	return f.name
}

// configureAddress устанавливает IPv4 адрес (Linux)
func configureAddress(name, addr string) error {
	cmd := execCommand("ip", "addr", "add", addr, "dev", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr add: %s: %w", string(output), err)
	}
	return nil
}

// configureAddress6 устанавливает IPv6 адрес (Linux)
func configureAddress6(name, addr string) error {
	cmd := execCommand("ip", "-6", "addr", "add", addr, "dev", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip -6 addr add: %s: %w", string(output), err)
	}
	return nil
}

// setMTU устанавливает MTU интерфейса (Linux)
func setMTU(name string, mtu int) error {
	cmd := execCommand("ip", "link", "set", "dev", name, "mtu", fmt.Sprintf("%d", mtu))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set mtu: %s: %w", string(output), err)
	}
	return nil
}

// setUp активирует интерфейс (Linux)
func setUp(name string) error {
	cmd := execCommand("ip", "link", "set", "dev", name, "up")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set up: %s: %w", string(output), err)
	}
	return nil
}
