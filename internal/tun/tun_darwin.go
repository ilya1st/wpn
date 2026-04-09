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

	// На macOS для utun (point-to-point) нужен destination адрес
	// Формат: ifconfig <iface> inet <local> <dest> netmask <mask>
	// Destination = первый usable IP в подсети (или адрес сервера)
	mask := net.IP(ipNet.Mask).String()
	dstIP := ipNet.IP.Mask(ipNet.Mask).To4()
	if dstIP != nil {
		// Increment network address by 1 to get first usable IP
		dstIP[3]++
	} else {
		// Fallback: same as local IP
		dstIP = ip.To4()
	}

	cmd := execCommand("ifconfig", name, "inet", ip.String(), dstIP.String(), "netmask", mask)
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
// utun на macOS добавляет 4-байтовый Protocol Family заголовок:
//   IPv4: 0x00000002 (AF_INET)
//   IPv6: 0x0000001E (AF_INET6)
// Мы strip/add этот заголовок чтобы обеспечить совместимость с Linux TUN (без заголовка)
type darwinFile struct {
	fd   int
	name string
}

const (
	utunHeaderLen = 4
	protoIPv4     = 0x00000002
	protoIPv6     = 0x0000001E
)

func (f *darwinFile) Read(b []byte) (int, error) {
	// Читаем в буфер с запасом под заголовок
	hdrBuf := make([]byte, utunHeaderLen+len(b))
	n, err := syscall.Read(f.fd, hdrBuf)
	if err != nil {
		return 0, err
	}
	if n < utunHeaderLen {
		return 0, fmt.Errorf("utun: short read (%d bytes)", n)
	}
	// Копируем только IP пакет (без 4-байтового заголовка)
	pktLen := n - utunHeaderLen
	copy(b, hdrBuf[utunHeaderLen:n])
	return pktLen, nil
}

func (f *darwinFile) Write(b []byte) (int, error) {
	// Определяем тип пакета по версии IP
	var proto uint32
	if len(b) > 0 && (b[0]>>4) == 6 {
		proto = protoIPv6
	} else {
		proto = protoIPv4
	}

	// Создаём буфер с заголовком
	hdrBuf := make([]byte, utunHeaderLen+len(b))
	// Записываем заголовок в big-endian
	hdrBuf[0] = 0
	hdrBuf[1] = 0
	hdrBuf[2] = uint8(proto >> 8)
	hdrBuf[3] = uint8(proto)
	// Копируем IP пакет
	copy(hdrBuf[utunHeaderLen:], b)

	n, err := syscall.Write(f.fd, hdrBuf)
	if err != nil {
		return 0, err
	}
	// Возвращаем длину записанных данных без заголовка
	return n - utunHeaderLen, nil
}

func (f *darwinFile) Close() error {
	return syscall.Close(f.fd)
}

func (f *darwinFile) Name() string {
	return f.name
}
