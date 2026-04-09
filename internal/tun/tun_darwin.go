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
	AF_SYSTEM        = 32
	AF_SYS_CONTROL   = 2
	SYSPROTO_CONTROL = 2
	SOCK_DGRAM       = 2
)

// sockaddr_ctl — структура для подключения к utun (macOS)
// struct sockaddr_ctl {
//     u_char      sc_len;       // 1
//     u_char      sc_family;    // 1
//     u_int16_t   ss_sysaddr;   // 2  (AF_SYS_CONTROL)
//     u_int32_t   sc_id;        // 4  (controller ID)
//     u_int32_t   sc_unit;      // 4  (unit number)
//     u_int32_t   sc_reserved[5];// 20
// }; // total: 32 bytes
type sockaddrCtl struct {
	scLen    uint8
	scFamily uint8
	scPort   uint16      // ss_sysaddr
	scID     uint32
	scUnit   uint32
	scResv   [5]uint32   // 20 bytes
}

// newTUN создаёт новый utun интерфейс (macOS)
func newTUN(name string) (fileInterface, string, error) {
	// Создаём сокет для системного контроллера через RawSyscall
	// syscall.Socket не поддерживает AF_SYSTEM на darwin
	fd, _, errno := syscall.RawSyscall(
		syscall.SYS_SOCKET,
		AF_SYSTEM,
		SOCK_DGRAM,
		SYSPROTO_CONTROL,
	)
	if errno != 0 {
		return nil, "", fmt.Errorf("socket AF_SYSTEM: %w", errno)
	}

	// Устанавливаем FD_CLOEXEC
	_, _, errno = syscall.RawSyscall(
		syscall.SYS_FCNTL,
		fd,
		uintptr(syscall.F_SETFD),
		uintptr(syscall.FD_CLOEXEC),
	)
	if errno != 0 {
		syscall.Close(int(fd))
		return nil, "", fmt.Errorf("fcntl FD_CLOEXEC: %w", errno)
	}

	// Получаем ID utun контроллера
	var info ctlInfo
	copy(info.Name[:], utunControlName)

	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(CTLIOCGINFO),
		uintptr(unsafe.Pointer(&info)),
	)
	if errno != 0 {
		syscall.Close(int(fd))
		return nil, "", fmt.Errorf("ioctl CTLIOCGINFO: %w", errno)
	}

	// Подключаемся к utun контроллеру
	sc := sockaddrCtl{
		scLen:    uint8(unsafe.Sizeof(sockaddrCtl{})), // 32
		scFamily: AF_SYSTEM,
		scPort:   AF_SYS_CONTROL, // ss_sysaddr, NOT 0
		scID:     info.ID,
		scUnit:   0, // 0 = автоматический выбор utun номер
	}

	_, _, errno = syscall.RawSyscall6(
		syscall.SYS_CONNECT,
		fd,
		uintptr(unsafe.Pointer(&sc)),
		uintptr(unsafe.Sizeof(sc)),
		0, 0, 0,
	)
	if errno != 0 {
		syscall.Close(int(fd))
		return nil, "", fmt.Errorf("connect utun: %w", errno)
	}

	// Получаем имя интерфейса
	var ifname [64]byte
	ifnameLen := uint64(len(ifname))
	_, _, errno = syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		fd,
		uintptr(2), // SYSPROTO_CONTROL
		uintptr(_UTUN_OPT_IFNAME),
		uintptr(unsafe.Pointer(&ifname[0])),
		uintptr(unsafe.Pointer(&ifnameLen)),
		0,
	)
	if errno != 0 {
		syscall.Close(int(fd))
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
	file := &darwinFile{fd: int(fd), name: actualName}
	return file, actualName, nil
}

// configureAddress устанавливает IPv4 адрес (macOS)
func configureAddress(name, addr string) error {
	// Разбираем адрес
	ip, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return fmt.Errorf("parse CIDR: %w", err)
	}

	// На macOS просто задаём IP и netmask
	mask := net.IP(ipNet.Mask).String()

	cmd := execCommand("ifconfig", name, "inet", ip.String(), "netmask", mask)
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
