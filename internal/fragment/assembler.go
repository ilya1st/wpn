package fragment

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/ilya1st/wpn/internal/protocol"
)

// FragmentGroup — группа фрагментов, ожидающих сборки
type FragmentGroup struct {
	FragmentID  uint32
	TotalFrags  uint16
	Received    uint16
	Fragments   map[uint16][]byte // FragmentNum -> data
	IsIPv6      bool
	Timer       *time.Timer
}

// AddFragment добавляет фрагмент в группу. Возвращает true, если группа собрана.
func (g *FragmentGroup) AddFragment(num uint16, data []byte) bool {
	if _, exists := g.Fragments[num]; exists {
		return false // дубликат
	}
	g.Fragments[num] = data
	g.Received++
	return g.Received == g.TotalFrags
}

// Assemble собирает полный пакет из фрагментов
func (g *FragmentGroup) Assemble() []byte {
	nums := make([]uint16, 0, len(g.Fragments))
	for n := range g.Fragments {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

	total := 0
	for _, n := range nums {
		total += len(g.Fragments[n])
	}

	result := make([]byte, 0, total)
	for _, n := range nums {
		result = append(result, g.Fragments[n]...)
	}
	return result
}

// Assembler управляет сборкой фрагментов
type Assembler struct {
	mu      sync.Mutex
	groups  map[uint32]*FragmentGroup // FragmentID -> группа
	timeout time.Duration
	onTimeout func(fragmentID uint32) // вызывается при таймауте
}

// NewAssembler создаёт сборщик фрагментов
func NewAssembler(timeout time.Duration, onTimeout func(uint32)) *Assembler {
	return &Assembler{
		groups:    make(map[uint32]*FragmentGroup),
		timeout:   timeout,
		onTimeout: onTimeout,
	}
}

// HandleFragment обрабатывает входящий фрагмент.
// Если группа собрана — возвращает полный пакет и isIPv6 флаг.
// Если не собрана — возвращает nil, nil.
func (a *Assembler) HandleFragment(msg *protocol.Message) (packet []byte, isIPv6 bool, complete bool) {
	header, data, err := protocol.ParseFragmentHeader(msg.Payload)
	if err != nil {
		log.Printf("Failed to parse fragment header: %v", err)
		return nil, false, false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	group, exists := a.groups[header.FragmentID]
	if !exists {
		// Первый фрагмент группы
		group = &FragmentGroup{
			FragmentID: header.FragmentID,
			TotalFrags: header.TotalFrags,
			Fragments:  make(map[uint16][]byte),
			IsIPv6:     msg.Flags&protocol.FlagIPv6 != 0,
		}
		a.groups[header.FragmentID] = group

		// Запускаем таймаут
		group.Timer = time.AfterFunc(a.timeout, func() {
			a.mu.Lock()
			delete(a.groups, header.FragmentID)
			a.mu.Unlock()
			if a.onTimeout != nil {
				a.onTimeout(header.FragmentID)
			}
		})
	}

	// Добавляем фрагмент
	complete = group.AddFragment(header.FragmentNum, data)
	isIPv6 = group.IsIPv6

	if complete {
		packet = group.Assemble()
		group.Timer.Stop()
		delete(a.groups, header.FragmentID)
	}

	return packet, isIPv6, complete
}

// Cleanup очищает все незавершённые группы (при закрытии соединения)
func (a *Assembler) Cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, g := range a.groups {
		g.Timer.Stop()
		delete(a.groups, id)
	}
}
