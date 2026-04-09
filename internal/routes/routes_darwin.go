//go:build darwin

package routes

import (
	"fmt"
	"os/exec"
)

// addRoute добавляет один маршрут через exec (macOS)
func (m *Manager) addRoute(route Route) error {
	args := []string{"-n", "add", "-net", route.Dst.String(), "-interface", route.Dev}
	if route.Metric > 0 {
		args = append(args, "-metric", fmt.Sprintf("%d", route.Metric))
	}

	cmd := exec.Command("route", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("route add: %s: %w", string(output), err)
	}
	return nil
}

// removeRoute удаляет один маршрут (macOS)
func (m *Manager) removeRoute(route Route) error {
	args := []string{"-n", "delete", "-net", route.Dst.String(), "-interface", route.Dev}
	cmd := exec.Command("route", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("route delete: %s: %w", string(output), err)
	}
	return nil
}

// removeAllRoutes удаляет все применённые маршруты (macOS)
func (m *Manager) removeAllRoutes() error {
	for _, route := range m.applied {
		if err := m.removeRoute(route); err != nil {
			fmt.Printf("Warning: failed to remove route %s: %v\n", route.Dst.String(), err)
		}
	}
	m.applied = nil
	return nil
}
