// Package session управляет сессиями клиентов: создание, хранение, пул адресов.
package session

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// SessionState — состояние сессии
type SessionState string

const (
	SessionActive      SessionState = "active"
	SessionReconnecting SessionState = "reconnecting"
	SessionExpired     SessionState = "expired"
)

// Session — информация о подключении клиента
type Session struct {
	ID             string          // UUID сессии
	Login          string          // логин клиента
	IsDynamicIP4   bool            // динамический IPv4
	IsDynamicIP6   bool            // динамический IPv6
	IP4            net.IP          // IPv4 туннеля
	IP6            net.IP          // IPv6 туннеля
	ClientIP       string          // последний IP подключения (внешний)
	WSConn         *websocket.Conn // WebSocket соединение
	State          SessionState
	LastActivity   time.Time // последняя активность
	DTStart        time.Time // старт сессии
	DTReconnect    time.Time // последний реконнект
	ReconnectCount int       // счётчик реконнектов
	BytesSent      uint64    // статистика
	BytesRecv      uint64
	PacketsSent    uint32
	PacketsRecv    uint32
	ClientVersion  string // версия клиента
	mu             sync.RWMutex
	// Канал для отправки данных клиенту (один writer goroutine читает)
	writeCh   chan []byte
	writeDone chan struct{}
}

// Lock блокирует мьютекс сессии
func (s *Session) Lock() {
	s.mu.Lock()
}

// Unlock разблокирует мьютекс сессии
func (s *Session) Unlock() {
	s.mu.Unlock()
}

// RLock блокирует мьютекс для чтения
func (s *Session) RLock() {
	s.mu.RLock()
}

// RUnlock разблокирует мьютекс чтения
func (s *Session) RUnlock() {
	s.mu.RUnlock()
}

// UpdateActivity обновляет timestamp последней активности
func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivity = time.Now()
}

// ConnectionState возвращает текущее состояние
func (s *Session) ConnectionState() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State
}

// SetConnectionState устанавливает состояние
func (s *Session) SetConnectionState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
}

// InitWriter инициализирует каналы writer'а (вызывается до StartWriter)
func (s *Session) InitWriter(chSize int) {
	s.writeCh = make(chan []byte, chSize)
	s.writeDone = make(chan struct{})
}

// StartWriter запускает горутину writer'а.
// keepaliveInterval — интервал отправки keepalive, 0 = отключён.
// onError — callback при ошибке записи (для логирования и сигнала остановки)
func (s *Session) StartWriter(keepaliveInterval time.Duration, onError func(error)) {
	go func() {
		var keepaliveTicker *time.Ticker
		var keepaliveCh <-chan time.Time
		if keepaliveInterval > 0 {
			keepaliveTicker = time.NewTicker(keepaliveInterval)
			defer keepaliveTicker.Stop()
			keepaliveCh = keepaliveTicker.C
		}

		keepaliveMsg := createKeepaliveMessageBytes()

		for {
			select {
			case data := <-s.writeCh:
				if err := s.writeToConn(data); err != nil {
					if onError != nil {
						onError(err)
					}
					return
				}
			case <-keepaliveCh:
				if err := s.writeToConn(keepaliveMsg); err != nil {
					if onError != nil {
						onError(err)
					}
					return
				}
			case <-s.writeDone:
				return
			}
		}
	}()
}

// StopWriter сигнализирует writer'е об остановке
func (s *Session) StopWriter() {
	select {
	case <-s.writeDone:
		// уже остановлен
	default:
		close(s.writeDone)
	}
}

// QueueWrite ставит данные в очередь на отправку.
// Неблокирующий: если канал полон — данные дропаются.
func (s *Session) QueueWrite(data []byte) bool {
	if s.writeCh == nil {
		return false
	}
	select {
	case s.writeCh <- data:
		return true
	default:
		// Канал полон — медленный клиент, дропаем
		return false
	}
}

// writeToConn безопасно пишет в WebSocket соединение
func (s *Session) writeToConn(data []byte) error {
	s.mu.RLock()
	conn := s.WSConn
	s.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("no websocket connection")
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// createKeepaliveMessageBytes создаёт бинарное представление KEEPALIVE
func createKeepaliveMessageBytes() []byte {
	header := make([]byte, 4)
	header[0] = 0x03 // MessageTypeKeepalive
	// Flags=0, PayloadLength=0
	return header
}

// Registry — реестр активных сессий
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session       // sessionID → Session
	byLogin  map[string][]*Session     // login → [Session]
	byIP4    map[string]*Session       // IPv4 string → Session
	byIP6    map[string]*Session       // IPv6 string → Session
	ip4Pool  *IPPool                   // пул IPv4 адресов
	ip6Pool  *IPPool                   // пул IPv6 адресов
}

// NewRegistry создаёт новый реестр сессий
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
		byLogin:  make(map[string][]*Session),
		byIP4:    make(map[string]*Session),
		byIP6:    make(map[string]*Session),
	}
}

// SetIPv4Pool устанавливает пул IPv4 адресов
func (r *Registry) SetIPv4Pool(pool *IPPool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ip4Pool = pool
}

// SetIPv6Pool устанавливает пул IPv6 адресов
func (r *Registry) SetIPv6Pool(pool *IPPool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ip6Pool = pool
}

// SetIPPools устанавливает оба пула
func (r *Registry) SetIPPools(ip4, ip6 *IPPool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ip4Pool = ip4
	r.ip6Pool = ip6
}

// CreateSession создаёт новую сессию и выдаёт адреса
func (r *Registry) CreateSession(login, clientIP string, conn *websocket.Conn, staticIP4, staticIP6 net.IP) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Выдаём IPv4
	var ip4 net.IP
	var isDynamicIP4 bool
	if len(staticIP4) > 0 {
		ip4 = make(net.IP, len(staticIP4))
		copy(ip4, staticIP4)
	} else if r.ip4Pool != nil {
		allocated, err := r.ip4Pool.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocate IPv4: %w", err)
		}
		ip4 = allocated
		isDynamicIP4 = true
	}

	// Выдаём IPv6
	var ip6 net.IP
	var isDynamicIP6 bool
	if len(staticIP6) > 0 {
		ip6 = make(net.IP, len(staticIP6))
		copy(ip6, staticIP6)
	} else if r.ip6Pool != nil {
		allocated, err := r.ip6Pool.Allocate()
		if err != nil {
			return nil, fmt.Errorf("allocate IPv6: %w", err)
		}
		ip6 = allocated
		isDynamicIP6 = true
	}

	sessionID := uuid.New().String()
	now := time.Now()

	s := &Session{
		ID:             sessionID,
		Login:          login,
		IsDynamicIP4:   isDynamicIP4,
		IsDynamicIP6:   isDynamicIP6,
		IP4:            ip4,
		IP6:            ip6,
		ClientIP:       clientIP,
		WSConn:         conn,
		State:          SessionActive,
		LastActivity:   now,
		DTStart:        now,
		DTReconnect:    time.Time{},
		ReconnectCount: 0,
	}

	r.sessions[sessionID] = s
	r.byLogin[login] = append(r.byLogin[login], s)

	// Регистрируем в IP-мапах для быстрого поиска
	if len(ip4) > 0 {
		r.byIP4[ip4.String()] = s
	}
	if len(ip6) > 0 {
		r.byIP6[ip6.String()] = s
	}

	return s, nil
}

// ReconnectSession восстанавливает сессию с новым соединением
func (r *Registry) ReconnectSession(sessionID, clientIP string, conn *websocket.Conn) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	s.mu.Lock()
	s.WSConn = conn
	s.ClientIP = clientIP
	s.State = SessionActive
	s.DTReconnect = time.Now()
	s.ReconnectCount++
	s.LastActivity = time.Now()
	s.mu.Unlock()

	return s, nil
}

// GetSession возвращает сессию по ID
func (r *Registry) GetSession(sessionID string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[sessionID]
}

// GetSessionsByLogin возвращает все сессии пользователя
func (r *Registry) GetSessionsByLogin(login string) []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sessions := r.byLogin[login]
	result := make([]*Session, len(sessions))
	copy(result, sessions)
	return result
}

// GetSessionByIP возвращает сессию по IP адресу (O(1) через hashmap)
func (r *Registry) GetSessionByIP(ip net.IP) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ip == nil {
		return nil
	}
	// Сначала ищем в IPv4
	if ip4 := ip.To4(); ip4 != nil {
		return r.byIP4[ip4.String()]
	}
	// Потом в IPv6
	return r.byIP6[ip.String()]
}

// RemoveSession удаляет сессию и освобождает адреса
func (r *Registry) RemoveSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[sessionID]
	if !ok {
		return
	}

	s.mu.Lock()
	s.State = SessionExpired
	s.mu.Unlock()

	// Освобождаем динамические адреса
	if s.IsDynamicIP4 && len(s.IP4) > 0 && r.ip4Pool != nil {
		r.ip4Pool.Release(s.IP4)
	}
	if s.IsDynamicIP6 && len(s.IP6) > 0 && r.ip6Pool != nil {
		r.ip6Pool.Release(s.IP6)
	}

	// Удаляем из IP-мапов
	if len(s.IP4) > 0 {
		delete(r.byIP4, s.IP4.String())
	}
	if len(s.IP6) > 0 {
		delete(r.byIP6, s.IP6.String())
	}

	// Удаляем из byLogin
	loginSessions := r.byLogin[s.Login]
	for i, sess := range loginSessions {
		if sess.ID == sessionID {
			r.byLogin[s.Login] = append(loginSessions[:i], loginSessions[i+1:]...)
			break
		}
	}

	delete(r.sessions, sessionID)
}

// ActiveSessions возвращает все активные сессии
func (r *Registry) ActiveSessions() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Session
	for _, s := range r.sessions {
		s.mu.RLock()
		if s.State == SessionActive {
			result = append(result, s)
		}
		s.mu.RUnlock()
	}
	return result
}

// CleanupExpired удаляет сессии в состоянии expired
func (r *Registry) CleanupExpired() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for id, s := range r.sessions {
		s.mu.RLock()
		expired := s.State == SessionExpired
		s.mu.RUnlock()
		if expired {
			delete(r.sessions, id)
			count++
		}
	}
	return count
}

// IPPool — пул IP адресов для динамической выдачи
type IPPool struct {
	mu      sync.Mutex
	network *net.IPNet
	used    map[string]bool
	nextIdx int
}

// NewIPPool создаёт пул из подсети
func NewIPPool(network *net.IPNet) *IPPool {
	return &IPPool{
		network: network,
		used:    make(map[string]bool),
		nextIdx: 2, // .0 — сеть, .1 — сервер
	}
}

// Allocate выделяет следующий свободный IP
func (p *IPPool) Allocate() (net.IP, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	maxIP := p.maxIP()
	if p.nextIdx >= maxIP {
		return nil, fmt.Errorf("IP pool exhausted")
	}

	ip := p.ipAt(p.nextIdx)
	for ip != nil && p.used[ip.String()] {
		p.nextIdx++
		if p.nextIdx >= maxIP {
			return nil, fmt.Errorf("IP pool exhausted")
		}
		ip = p.ipAt(p.nextIdx)
	}

	if ip == nil {
		return nil, fmt.Errorf("failed to generate IP")
	}

	p.used[ip.String()] = true
	p.nextIdx++
	return ip, nil
}

// Release освобождает IP
func (p *IPPool) Release(ip net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, ip.String())
}

// IsUsed проверяет занятость адреса
func (p *IPPool) IsUsed(ip net.IP) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.used[ip.String()]
}

// ipAt генерирует IP по индексу
func (p *IPPool) ipAt(idx int) net.IP {
	if len(p.network.IP) == 4 {
		// IPv4
		base := p.network.IP.To4()
		if base == nil {
			return nil
		}
		ip := make(net.IP, 4)
		copy(ip, base)
		// Для /24 меняем последний октет
		ones, _ := p.network.Mask.Size()
		hostBits := 32 - ones
		if hostBits == 8 {
			ip[3] = byte(idx)
		} else {
			// Для других масок
			ip[3] = byte(idx)
		}
		if !p.network.Contains(ip) {
			return nil
		}
		return ip
	}
	// IPv6 — упрощённо: меняем последний байт
	ip := make(net.IP, 16)
	copy(ip, p.network.IP.To16())
	ip[15] = byte(idx)
	if !p.network.Contains(ip) {
		return nil
	}
	return ip
}

// maxIP возвращает максимальный индекс хоста
func (p *IPPool) maxIP() int {
	ones, bits := p.network.Mask.Size()
	hostBits := bits - ones
	// Резервируем 1 адрес для сервера
	max := (1 << uint(hostBits)) - 2
	if max > 254 {
		max = 254
	}
	return max
}
