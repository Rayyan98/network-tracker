package flow

import "time"

// MetricSample represents a single measurement from a flow at a point in time.
type MetricSample struct {
	Timestamp     time.Time
	UploadBytes   int
	DownloadBytes int
	RTT           time.Duration // zero means no RTT sample
	IsRetrans     bool
	IsSegment     bool   // true if this was a data-carrying TCP segment
	RemoteIP      string // remote endpoint IP
	LocalPort     uint16 // local port (used for process lookup)
	Process       string // process name (filled by caller)
	IsUDP         bool   // true if this was a UDP packet
	DNSLatency    time.Duration // DNS query->response time (if applicable)
}
