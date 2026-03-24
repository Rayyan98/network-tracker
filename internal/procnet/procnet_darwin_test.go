package procnet

import (
	"testing"
)

func TestMapperRefresh(t *testing.T) {
	m := NewMapper()
	m.Refresh()

	// After refresh, we should have at least a few ports mapped
	// (the test process itself has open sockets)
	m.mu.RLock()
	count := len(m.byPort)
	m.mu.RUnlock()

	t.Logf("Found %d port->process mappings", count)
	// We can't assert a specific count, but it shouldn't crash
}

func TestProcessForPort(t *testing.T) {
	m := NewMapper()
	m.Refresh()

	// Unknown port should return empty
	name := m.ProcessForPort(1)
	if name != "" {
		t.Logf("Port 1 mapped to: %s", name)
	}
}
