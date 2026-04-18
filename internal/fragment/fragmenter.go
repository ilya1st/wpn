// Package fragment предоставляет механизмы фрагментации и сборки пакетов
package fragment

import (
	"sync"

	"github.com/ilya1st/wpn/internal/protocol"
)

// Fragmenter управляет отправкой фрагментов одного соединения
type Fragmenter struct {
	mu       sync.Mutex
	nextID   uint32
	maxData  int // MaxFragmentDataSize
}

// NewFragmenter создаёт фрагментер
func NewFragmenter() *Fragmenter {
	return &Fragmenter{
		nextID: 1,
		maxData: protocol.MaxFragmentDataSize,
	}
}

// Fragment разбивает пакет на FRAGMENT сообщения.
// Если пакет <= maxData, возвращает nil (отправлять как DATA).
func (f *Fragmenter) Fragment(packet []byte, isIPv6 bool) []*protocol.Message {
	f.mu.Lock()
	fragmentID := f.nextID
	f.nextID++
	f.mu.Unlock()

	if len(packet) <= f.maxData {
		return nil
	}

	totalFrags := (len(packet) + f.maxData - 1) / f.maxData
	msgs := make([]*protocol.Message, 0, totalFrags)

	for i := 0; i < totalFrags; i++ {
		start := i * f.maxData
		end := start + f.maxData
		if end > len(packet) {
			end = len(packet)
		}

		msg := protocol.CreateFragmentMessage(fragmentID, uint16(i), uint16(totalFrags), packet[start:end])

		// Передаём флаг IPv6 в первом фрагменте
		if i == 0 && isIPv6 {
			msg.Flags |= protocol.FlagIPv6
		}

		msgs = append(msgs, msg)
	}

	return msgs
}

// NeedsFragment проверяет, нужно ли разбивать пакет
func (f *Fragmenter) NeedsFragment(packetLen int) bool {
	return packetLen > f.maxData
}
