//go:build linux

package routes

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// addRoute добавляет один маршрут через netlink
func (m *Manager) addRoute(route Route) error {
	link, err := netlink.LinkByName(m.devName)
	if err != nil {
		return fmt.Errorf("get link %s: %w", m.devName, err)
	}

	routeNetlink := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       route.Dst,
		Priority:  route.Metric,
	}

	if route.GW != nil {
		routeNetlink.Gw = route.GW
	}

	if err := netlink.RouteAdd(routeNetlink); err != nil {
		return fmt.Errorf("route add: %w", err)
	}

	return nil
}

// removeRoute удаляет один маршрут
func (m *Manager) removeRoute(route Route) error {
	link, err := netlink.LinkByName(m.devName)
	if err != nil {
		return fmt.Errorf("get link %s: %w", m.devName, err)
	}

	routeNetlink := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       route.Dst,
		Priority:  route.Metric,
	}

	if route.GW != nil {
		routeNetlink.Gw = route.GW
	}

	if err := netlink.RouteDel(routeNetlink); err != nil {
		return fmt.Errorf("route del: %w", err)
	}

	return nil
}

// removeAllRoutes удаляет все применённые маршруты
func (m *Manager) removeAllRoutes() error {
	for _, route := range m.applied {
		if err := m.removeRoute(route); err != nil {
			fmt.Printf("Warning: failed to remove route %s: %v\n", route.Dst.String(), err)
		}
	}
	m.applied = nil
	return nil
}
