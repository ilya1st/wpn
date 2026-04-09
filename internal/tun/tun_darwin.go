//go:build darwin

package tun

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const (
	utunControlName = "com.apple.net.utun_control"
	ifNameSize      = 16
)

// ctlInfo информация о control
type ctlInfo struct {
	ID   uint32
	Name [96]byte
}

const (
	_UTUN_OPT_IFNAME = 2
	CTLIOCGINFO      = 0xc0644e03 // CTLIOCGINFO ioctl code
	AF_SYS_CONTROL   = 2
	SYSPROTO_CONTROL = 2
)

// sockaddr_ctl структура для подключения к utun
type sockaddrCtl struct {
	scLen    uint8
	scFamily uint8
	scPort   uint16
	scID     uint32
	scUnit   uint32
	scResv   [64]byte
}

// newTUN создаёт новый utun интерфейс (macOS)
func newTUN(name string) (fileInterface, string, error) {
	// Создаём сокет для системного контроллера
	fd, err := syscall.Socket(AF_SYS_CONTROL, syscall.SOCK_DGRAM, SYSPROTO_CONTROL)
	if err != nil {
		return nil, "", fmt.Errorf("socket AF_SYSTEM: %w", err)
	}

	// Получаем ID utun контроллера
	var info ctlInfo
	copy(info.Name[:], utunControlName)

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(CTLIOCGINFO),
		uintptr(unsafe.Pointer(&info)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, "", fmt.Errorf("ioctl CTLIOCGINFO: %w", errno)
	}

	// Подключаемся к utun контроллеру
	sc := sockaddrCtl{
		scLen:    32,
		scFamily: AF_SYS_CONTROL,
		scPort:   SYSPROTO_CONTROL,
		scID:     info.ID,
		scUnit:   0, // 0 = автоматический выбор utun номер
	}

	_, _, errno = syscall.RawSyscall6(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sc)),
		uintptr(unsafe.Sizeof(sc)),
		0, 0, 0,
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, "", fmt.Errorf("connect utun: %w", errno)
	}

	// Получаем имя интерфейса
	var ifname [64]byte
	ifnameLen := uint64(len(ifname))
	_, _, errno = syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(2), // SYSPROTO_CONTROL
		uintptr(_UTUN_OPT_IFNAME),
		uintptr(unsafe.Pointer(&ifname[0])),
		uintptr(unsafe.Pointer(&ifnameLen)),
		0,
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, "", fmt.Errorf("getsockopt UTUN_OPT_IFNAME: %w", errno)
	}

	// Конвертируем имя
	actualName := string(ifname[:ifnameLen])
	// Убираем нулевые байты
	for i, b := range actualName {
		if b == 0 {
			actualName = actualName[:i]
			break
		}
	}

	// Оборачиваем fd в интерфейс для Read/Write
	file := &darwinFile{fd: fd, name: actualName}
	return file, actualName, nil
}

// configureAddress устанавливает IPv4 адрес (macOS)
func configureAddress(name, addr string) error {
	// Разбираем адрес
	ip, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return fmt.Errorf("parse CIDR: %w", err)
	}

	// Получаем адрес сети в формате x.x.x.x
	dstIP := ipNet.IP.Mask(ipNet.Mask)

	// Используем ifconfig для установки адреса
	cmd := execCommand("ifconfig", name, "inet", ip.String(), "netmask", net.IP(ipNet.Mask).String(), dstIP.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig inet: %s: %w", string(output), err)
	}
	return nil
}

// configureAddress6 устанавливает IPv6 адрес (macOS)
func configureAddress6(name, addr string) error {
	cmd := execCommand("ifconfig", name, "inet6", addr, "add")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig inet6: %s: %w", string(output), err)
	}
	return nil
}

// setMTU устанавливает MTU интерфейса (macOS)
func setMTU(name string, mtu int) error {
	cmd := execCommand("ifconfig", name, "mtu", fmt.Sprintf("%d", mtu))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig mtu: %s: %w", string(output), err)
	}
	return nil
}

// setUp активирует интерфейс (macOS)
func setUp(name string) error {
	cmd := execCommand("ifconfig", name, "up")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig up: %s: %w", string(output), err)
	}
	return nil
}

// darwinFile реализация fileInterface для macOS
type darwinFile struct {
	fd   int
	name string
}

func (f *darwinFile) Read(b []byte) (int, error) {
	n, err := syscall.Read(f.fd, b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (f *darwinFile) Write(b []byte) (int, error) {
	n, err := syscall.Write(f.fd, b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (f *darwinFile) Close() error {
	return syscall.Close(f.fd)
}

func (f *darwinFile) Name() string {
	return f.name
}
