package capture

import (
	"net"
	"sync"
	"time"
)

type TrafficFilter struct {
	excludeNets []*net.IPNet
	localNets   []*net.IPNet
	localIPs    map[string]bool
	mu          sync.RWMutex
}

func NewTrafficFilter(excludeNetworks, localNetworks []string) (*TrafficFilter, error) {
	f := &TrafficFilter{
		localIPs: make(map[string]bool),
	}

	for _, cidr := range excludeNetworks {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		f.excludeNets = append(f.excludeNets, ipNet)
	}

	for _, cidr := range localNetworks {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		f.localNets = append(f.localNets, ipNet)
	}

	f.refreshLocalIPs()
	return f, nil
}

func (f *TrafficFilter) refreshLocalIPs() {
	ips := make(map[string]bool)
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				ips[ipNet.IP.String()] = true
			}
		}
	}
	f.mu.Lock()
	f.localIPs = ips
	f.mu.Unlock()
}

// StartRefresh periodically refreshes local IP list.
func (f *TrafficFilter) StartRefresh(done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			f.refreshLocalIPs()
		case <-done:
			return
		}
	}
}

// IsLocal returns true if the IP is a local interface IP.
func (f *TrafficFilter) IsLocal(ip net.IP) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.localIPs[ip.String()]
}

// ShouldExclude returns true if this packet should be dropped.
// A packet is excluded if:
// - either endpoint is in an excluded network
// - both endpoints are local (intra-machine traffic)
// - both endpoints are in local networks (e.g. Docker bridge)
func (f *TrafficFilter) ShouldExclude(srcIP, dstIP net.IP) bool {
	for _, n := range f.excludeNets {
		if n.Contains(srcIP) || n.Contains(dstIP) {
			return true
		}
	}

	srcLocal := f.IsLocal(srcIP)
	dstLocal := f.IsLocal(dstIP)
	if srcLocal && dstLocal {
		return true
	}

	// Both in private/local networks but neither is our machine = container-to-container
	srcInLocal := f.inLocalNets(srcIP)
	dstInLocal := f.inLocalNets(dstIP)
	if srcInLocal && dstInLocal && !srcLocal && !dstLocal {
		return true
	}

	return false
}

// Direction returns "upload" if src is local, "download" if dst is local,
// or empty string if neither (shouldn't happen after filtering).
func (f *TrafficFilter) Direction(srcIP, dstIP net.IP) string {
	if f.IsLocal(srcIP) {
		return "upload"
	}
	if f.IsLocal(dstIP) {
		return "download"
	}
	return ""
}

func (f *TrafficFilter) inLocalNets(ip net.IP) bool {
	for _, n := range f.localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
