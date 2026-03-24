package capture

import (
	"net"
	"testing"
)

func TestExcludeLoopback(t *testing.T) {
	f, err := NewTrafficFilter(
		[]string{"127.0.0.0/8", "169.254.0.0/16"},
		[]string{"10.0.0.0/8", "192.168.0.0/16"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !f.ShouldExclude(net.ParseIP("127.0.0.1"), net.ParseIP("8.8.8.8")) {
		t.Error("loopback should be excluded")
	}
	if !f.ShouldExclude(net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")) {
		t.Error("loopback dst should be excluded")
	}
}

func TestExcludeLinkLocal(t *testing.T) {
	f, _ := NewTrafficFilter(
		[]string{"169.254.0.0/16"},
		nil,
	)
	if !f.ShouldExclude(net.ParseIP("169.254.1.1"), net.ParseIP("8.8.8.8")) {
		t.Error("link-local should be excluded")
	}
}

func TestExcludeDockerBridge(t *testing.T) {
	f, _ := NewTrafficFilter(
		[]string{"172.17.0.0/16"},
		nil,
	)
	if !f.ShouldExclude(net.ParseIP("172.17.0.2"), net.ParseIP("172.17.0.3")) {
		t.Error("docker bridge traffic should be excluded")
	}
}

func TestAllowExternalTraffic(t *testing.T) {
	f, _ := NewTrafficFilter(
		[]string{"127.0.0.0/8", "169.254.0.0/16", "172.17.0.0/16"},
		[]string{"10.0.0.0/8", "192.168.0.0/16"},
	)

	// Simulate: our machine is 192.168.1.100, talking to 8.8.8.8
	f.localIPs = map[string]bool{"192.168.1.100": true}

	if f.ShouldExclude(net.ParseIP("192.168.1.100"), net.ParseIP("8.8.8.8")) {
		t.Error("local -> external should NOT be excluded")
	}
	if f.ShouldExclude(net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.100")) {
		t.Error("external -> local should NOT be excluded")
	}
}

func TestExcludeBothLocal(t *testing.T) {
	f, _ := NewTrafficFilter(nil, nil)
	f.localIPs = map[string]bool{
		"192.168.1.100": true,
		"192.168.1.101": true,
	}

	if !f.ShouldExclude(net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.101")) {
		t.Error("both-local should be excluded")
	}
}

func TestDirection(t *testing.T) {
	f, _ := NewTrafficFilter(nil, nil)
	f.localIPs = map[string]bool{"192.168.1.100": true}

	if d := f.Direction(net.ParseIP("192.168.1.100"), net.ParseIP("8.8.8.8")); d != "upload" {
		t.Errorf("expected upload, got %s", d)
	}
	if d := f.Direction(net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.100")); d != "download" {
		t.Errorf("expected download, got %s", d)
	}
}
