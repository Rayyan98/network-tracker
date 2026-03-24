package capture

import (
	"fmt"
	"net"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/rewaa/network-tracker/internal/config"
)

type Protocol uint8

const (
	ProtoTCP Protocol = 1
	ProtoUDP Protocol = 2
)

type Packet struct {
	Timestamp  time.Time
	SrcIP      net.IP
	DstIP      net.IP
	SrcPort    uint16
	DstPort    uint16
	Protocol   Protocol
	TCP        *layers.TCP // nil for UDP
	PayloadLen int
	Direction  string // "upload" or "download"
	IsDNS      bool   // UDP port 53
}

type Engine struct {
	cfg    config.CaptureConfig
	filter *TrafficFilter
	handle *pcap.Handle
}

func NewEngine(cfg config.CaptureConfig, filterCfg config.FilterConfig) (*Engine, error) {
	f, err := NewTrafficFilter(filterCfg.ExcludeNetworks, filterCfg.LocalNetworks)
	if err != nil {
		return nil, fmt.Errorf("creating traffic filter: %w", err)
	}
	return &Engine{cfg: cfg, filter: f}, nil
}

func (e *Engine) Start(packets chan<- Packet, done <-chan struct{}) error {
	handle, err := pcap.OpenLive(
		e.cfg.Interface,
		e.cfg.SnapLen,
		e.cfg.Promiscuous,
		pcap.BlockForever,
	)
	if err != nil {
		return fmt.Errorf("opening interface %s: %w", e.cfg.Interface, err)
	}
	e.handle = handle

	// BPF filter: TCP + UDP, no loopback, no link-local, no multicast
	bpf := "(tcp or udp) and not net 127.0.0.0/8 and not net 169.254.0.0/16 and not net 224.0.0.0/4"
	if err := handle.SetBPFFilter(bpf); err != nil {
		handle.Close()
		return fmt.Errorf("setting BPF filter: %w", err)
	}

	go e.filter.StartRefresh(done)
	go e.captureLoop(packets, done)
	return nil
}

func (e *Engine) captureLoop(packets chan<- Packet, done <-chan struct{}) {
	defer e.handle.Close()

	source := gopacket.NewPacketSource(e.handle, e.handle.LinkType())
	source.NoCopy = true

	for {
		select {
		case <-done:
			return
		default:
		}

		pkt, err := source.NextPacket()
		if err != nil {
			continue
		}

		p, ok := e.parsePacket(pkt)
		if !ok {
			continue
		}

		select {
		case packets <- p:
		case <-done:
			return
		}
	}
}

func (e *Engine) parsePacket(pkt gopacket.Packet) (Packet, bool) {
	networkLayer := pkt.NetworkLayer()
	if networkLayer == nil {
		return Packet{}, false
	}

	var srcIP, dstIP net.IP
	var ipTotalLen int
	var ipHeaderLen int

	switch nl := networkLayer.(type) {
	case *layers.IPv4:
		srcIP = nl.SrcIP
		dstIP = nl.DstIP
		ipTotalLen = int(nl.Length)
		ipHeaderLen = int(nl.IHL) * 4
	case *layers.IPv6:
		srcIP = nl.SrcIP
		dstIP = nl.DstIP
		ipTotalLen = int(nl.Length) + 40
		ipHeaderLen = 40
	default:
		return Packet{}, false
	}

	if e.filter.ShouldExclude(srcIP, dstIP) {
		return Packet{}, false
	}

	dir := e.filter.Direction(srcIP, dstIP)
	if dir == "" {
		return Packet{}, false
	}

	ts := pkt.Metadata().Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	// Try TCP
	if tcpLayer := pkt.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp, _ := tcpLayer.(*layers.TCP)
		tcpHeaderLen := int(tcp.DataOffset) * 4
		payloadLen := ipTotalLen - ipHeaderLen - tcpHeaderLen
		if payloadLen < 0 {
			payloadLen = 0
		}
		return Packet{
			Timestamp:  ts,
			SrcIP:      srcIP,
			DstIP:      dstIP,
			SrcPort:    uint16(tcp.SrcPort),
			DstPort:    uint16(tcp.DstPort),
			Protocol:   ProtoTCP,
			TCP:        tcp,
			PayloadLen: payloadLen,
			Direction:  dir,
		}, true
	}

	// Try UDP
	if udpLayer := pkt.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp, _ := udpLayer.(*layers.UDP)
		payloadLen := ipTotalLen - ipHeaderLen - 8 // UDP header is always 8 bytes
		if payloadLen < 0 {
			payloadLen = 0
		}
		isDNS := uint16(udp.SrcPort) == 53 || uint16(udp.DstPort) == 53
		return Packet{
			Timestamp:  ts,
			SrcIP:      srcIP,
			DstIP:      dstIP,
			SrcPort:    uint16(udp.SrcPort),
			DstPort:    uint16(udp.DstPort),
			Protocol:   ProtoUDP,
			PayloadLen: payloadLen,
			Direction:  dir,
			IsDNS:      isDNS,
		}, true
	}

	return Packet{}, false
}

func (e *Engine) Interface() string {
	return e.cfg.Interface
}
