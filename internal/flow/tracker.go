package flow

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rewaa/network-tracker/internal/capture"
)

const flowTimeout = 2 * time.Minute

// FlowKey uniquely identifies a connection (normalized so lower IP:port is always first).
type FlowKey struct {
	IP1   string
	Port1 uint16
	IP2   string
	Port2 uint16
	Proto capture.Protocol
}

func makeFlowKey(srcIP, dstIP net.IP, srcPort, dstPort uint16, proto capture.Protocol) FlowKey {
	src := fmt.Sprintf("%s:%d", srcIP, srcPort)
	dst := fmt.Sprintf("%s:%d", dstIP, dstPort)
	if src < dst {
		return FlowKey{srcIP.String(), srcPort, dstIP.String(), dstPort, proto}
	}
	return FlowKey{dstIP.String(), dstPort, srcIP.String(), srcPort, proto}
}

type flowState struct {
	rtt      *rttTracker
	loss     *lossTracker
	speed    *speedTracker
	lastSeen time.Time
}

// dnsQuery tracks pending DNS requests for latency measurement.
type dnsQuery struct {
	timestamp time.Time
}

// Tracker tracks TCP/UDP flows and produces metric samples.
type Tracker struct {
	mu    sync.Mutex
	flows map[FlowKey]*flowState

	// DNS latency tracking: (srcIP:srcPort) -> query send time
	dnsMu      sync.Mutex
	dnsQueries map[string]dnsQuery
}

func NewTracker() *Tracker {
	return &Tracker{
		flows:      make(map[FlowKey]*flowState),
		dnsQueries: make(map[string]dnsQuery),
	}
}

// Process handles a captured packet and returns a MetricSample.
func (t *Tracker) Process(pkt capture.Packet) MetricSample {
	isUpload := pkt.Direction == "upload"

	var remoteIP string
	var localPort uint16
	if isUpload {
		remoteIP = pkt.DstIP.String()
		localPort = pkt.SrcPort
	} else {
		remoteIP = pkt.SrcIP.String()
		localPort = pkt.DstPort
	}

	sample := MetricSample{
		Timestamp: pkt.Timestamp,
		RemoteIP:  remoteIP,
		LocalPort: localPort,
		IsUDP:     pkt.Protocol == capture.ProtoUDP,
	}

	// Track bytes
	if isUpload {
		sample.UploadBytes = pkt.PayloadLen
	} else {
		sample.DownloadBytes = pkt.PayloadLen
	}

	// Handle UDP — also track transfer speed for UDP flows
	if pkt.Protocol == capture.ProtoUDP {
		if pkt.IsDNS {
			sample.DNSLatency = t.trackDNS(pkt, isUpload)
		}
		// Track UDP flow speed
		key := makeFlowKey(pkt.SrcIP, pkt.DstIP, pkt.SrcPort, pkt.DstPort, pkt.Protocol)
		t.mu.Lock()
		fs, exists := t.flows[key]
		if !exists {
			fs = &flowState{
				rtt:   newRTTTracker(),
				loss:  newLossTracker(),
				speed: newSpeedTracker(),
			}
			t.flows[key] = fs
		}
		fs.lastSeen = pkt.Timestamp
		fs.speed.RecordData(pkt.Timestamp, sample.UploadBytes, sample.DownloadBytes)
		sample.DownloadSpeeds, sample.UploadSpeeds = fs.speed.DrainSpeeds()
		t.mu.Unlock()
		return sample
	}

	// TCP flow tracking
	key := makeFlowKey(pkt.SrcIP, pkt.DstIP, pkt.SrcPort, pkt.DstPort, pkt.Protocol)
	tcp := pkt.TCP

	t.mu.Lock()
	fs, exists := t.flows[key]
	if !exists {
		fs = &flowState{
			rtt:   newRTTTracker(),
			loss:  newLossTracker(),
			speed: newSpeedTracker(),
		}
		t.flows[key] = fs
	}
	fs.lastSeen = pkt.Timestamp
	t.mu.Unlock()

	// SYN handling
	if tcp.SYN && !tcp.ACK && isUpload {
		fs.rtt.RecordSYN(pkt.Timestamp)
	}

	// SYN-ACK handling
	if tcp.SYN && tcp.ACK && !isUpload {
		rtt := fs.rtt.RecordSYNACK(pkt.Timestamp)
		if rtt > 0 {
			sample.RTT = rtt
		}
	}

	// Data segment handling
	if pkt.PayloadLen > 0 {
		sample.IsSegment = true
		if isUpload {
			isRetrans := fs.loss.RecordOutgoing(tcp.Seq, pkt.PayloadLen)
			sample.IsRetrans = isRetrans
			if isRetrans {
				fs.rtt.RecordRetransmit(tcp.Seq)
			} else {
				fs.rtt.RecordSend(tcp.Seq, pkt.Timestamp)
			}
		} else {
			isRetrans := fs.loss.RecordIncoming(tcp.Seq, pkt.PayloadLen)
			sample.IsRetrans = isRetrans
		}
	}

	// ACK handling (for RTT calculation)
	if tcp.ACK && !tcp.SYN && !isUpload {
		rtt := fs.rtt.RecordACK(tcp.Ack, pkt.Timestamp)
		if rtt > 0 {
			sample.RTT = rtt
		}
	}

	// Duplicate ACK tracking
	if tcp.ACK {
		fs.loss.RecordACK(tcp.Ack, isUpload, pkt.PayloadLen)
	}

	// Transfer speed tracking
	if pkt.PayloadLen > 0 {
		fs.speed.RecordData(pkt.Timestamp, sample.UploadBytes, sample.DownloadBytes)
	}
	sample.DownloadSpeeds, sample.UploadSpeeds = fs.speed.DrainSpeeds()

	return sample
}

// trackDNS matches DNS queries and responses to measure latency.
func (t *Tracker) trackDNS(pkt capture.Packet, isUpload bool) time.Duration {
	if isUpload && pkt.DstPort == 53 {
		// Outgoing DNS query — record it
		key := fmt.Sprintf("%s:%d", pkt.SrcIP, pkt.SrcPort)
		t.dnsMu.Lock()
		t.dnsQueries[key] = dnsQuery{timestamp: pkt.Timestamp}
		t.dnsMu.Unlock()
		return 0
	}

	if !isUpload && pkt.SrcPort == 53 {
		// Incoming DNS response — match to query
		key := fmt.Sprintf("%s:%d", pkt.DstIP, pkt.DstPort)
		t.dnsMu.Lock()
		q, found := t.dnsQueries[key]
		if found {
			delete(t.dnsQueries, key)
		}
		t.dnsMu.Unlock()
		if found {
			return pkt.Timestamp.Sub(q.timestamp)
		}
	}
	return 0
}

// ActiveFlows returns the number of currently tracked flows.
func (t *Tracker) ActiveFlows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.flows)
}

// Cleanup removes flows that have been inactive for longer than the timeout.
func (t *Tracker) Cleanup(now time.Time) int {
	t.mu.Lock()
	removed := 0
	for key, fs := range t.flows {
		if now.Sub(fs.lastSeen) > flowTimeout {
			delete(t.flows, key)
			removed++
		}
	}
	t.mu.Unlock()

	// Clean stale DNS queries (older than 10 seconds)
	t.dnsMu.Lock()
	for key, q := range t.dnsQueries {
		if now.Sub(q.timestamp) > 10*time.Second {
			delete(t.dnsQueries, key)
		}
	}
	t.dnsMu.Unlock()

	return removed
}

// StartCleanup runs periodic flow cleanup.
func (t *Tracker) StartCleanup(done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.Cleanup(time.Now())
		case <-done:
			return
		}
	}
}
