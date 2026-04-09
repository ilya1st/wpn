package routes

import (
	"fmt"
	"net"
	"sort"

	"github.com/iazarov/vpn/internal/config"
)

// Route представляет маршрут
type Route struct {
	Dst    *net.IPNet
	GW     net.IP
	Metric int
	Dev    string // имя интерфейса
}

// Manager управляет маршрутами
type Manager struct {
	routes      []Route
	applied     []Route
	devName     string
}

// NewManager создаёт новый менеджер маршрутов
func NewManager(devName string) *Manager {
	return &Manager{
		devName: devName,
	}
}

// AddRoute добавляет маршрут
func (m *Manager) AddRoute(route Route) {
	route.Dev = m.devName
	m.routes = append(m.routes, route)
}

// AddRoutes добавляет несколько маршрутов
func (m *Manager) AddRoutes(routes []Route) {
	for _, r := range routes {
		r.Dev = m.devName
		m.routes = append(m.routes, r)
	}
}

// ApplyRoutes применяет все маршруты к интерфейсу
// Маршруты с меньшим metric имеют больший приоритет
func (m *Manager) ApplyRoutes() error {
	// Сортировка по метрике (меньше = приоритетнее)
	sort.Slice(m.routes, func(i, j int) bool {
		return m.routes[i].Metric < m.routes[j].Metric
	})

	// Удаление старых маршрутов
	if err := m.removeAllRoutes(); err != nil {
		return fmt.Errorf("remove old routes: %w", err)
	}

	// Применение новых маршрутов
	for _, route := range m.routes {
		if err := m.addRoute(route); err != nil {
			return fmt.Errorf("add route %s: %w", route.Dst.String(), err)
		}
		m.applied = append(m.applied, route)
	}

	return nil
}

// ClearRoutes удаляет все применённые маршруты
func (m *Manager) ClearRoutes() error {
	for _, route := range m.applied {
		if err := m.removeRoute(route); err != nil {
			// Не критичная ошибка, продолжаем
			fmt.Printf("Warning: failed to remove route %s: %v\n", route.Dst.String(), err)
		}
	}
	m.applied = nil
	m.routes = nil
	return nil
}

// MergeWithServerRoutes объединяет клиентские маршруты с серверными
// Клиентские маршруты имеют больший приоритет (меньшая метрика)
func MergeWithServerRoutes(clientRoutes, serverRoutes []Route) []Route {
	// Увеличиваем метрику серверных маршрутов чтобы они были менее приоритетными
	adjustedServer := make([]Route, len(serverRoutes))
	for i, r := range serverRoutes {
		r.Metric += 1000 // Сдвиг для понижения приоритета
		adjustedServer[i] = r
	}

	// Объединяем
	result := make([]Route, 0, len(clientRoutes)+len(adjustedServer))
	result = append(result, clientRoutes...)
	result = append(result, adjustedServer...)

	return result
}

// ListRoutes возвращает список текущих маршрутов
func (m *Manager) ListRoutes() []Route {
	return m.routes
}

// ListAppliedRoutes возвращает список применённых маршрутов
func (m *Manager) ListAppliedRoutes() []Route {
	return m.applied
}

// ParseRoutesFromConfig парсит маршруты из конфига
func ParseRoutesFromConfig(entries []config.RouteEntry, devName string) ([]Route, error) {
	result := make([]Route, 0, len(entries))

	for _, entry := range entries {
		_, dst, err := net.ParseCIDR(entry.Dst)
		if err != nil {
			return nil, fmt.Errorf("parse dst %s: %w", entry.Dst, err)
		}

		var gw net.IP
		if entry.GW != "" {
			gw = net.ParseIP(entry.GW)
			if gw == nil {
				return nil, fmt.Errorf("invalid gw IP: %s", entry.GW)
			}
		}

		result = append(result, Route{
			Dst:    dst,
			GW:     gw,
			Metric: entry.Metric,
			Dev:    devName,
		})
	}

	return result, nil
}
