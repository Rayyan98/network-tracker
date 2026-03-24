package aggregate

// AggregatedSample represents metrics for a single time bucket.
type AggregatedSample struct {
	Timestamp   int64   // Unix timestamp (bucket boundary)
	UploadBPS   float64 // bytes per second (TCP + UDP)
	DownloadBPS float64 // bytes per second (TCP + UDP)
	RTTAvgMs    float64 // average RTT in milliseconds (0 if no samples)
	RTTP95Ms    float64 // p95 RTT in milliseconds
	RTTMaxMs    float64 // max RTT in milliseconds
	JitterMs    float64 // RTT jitter (std deviation of RTT samples)
	LossPct     float64 // packet loss percentage (0-100)
	ActiveFlows int
	RTTCount    int // number of RTT samples (0 means no RTT data)
	Segments    int // total TCP data segments
	Retrans     int // retransmitted TCP segments

	// UDP metrics
	UDPUploadBPS   float64
	UDPDownloadBPS float64

	// DNS
	DNSAvgMs float64 // average DNS resolution time in ms
	DNSMaxMs float64
	DNSCount int

	// Transfer speed: actual speed data moves at during bursts (bytes/sec)
	// This is different from throughput — it measures link capacity when active.
	TransferDownBPS float64 // p90 download burst speed
	TransferUpBPS   float64 // p90 upload burst speed
	TransferCount   int     // number of burst samples

	// Quality
	QualityScore  int            // 0-100 composite score
	UseCaseStatus map[string]int // use case name -> status (0=bad, 1=degraded, 2=good)

	// Per-key breakdowns (populated during flush)
	ByApp  map[string]*Breakdown // process name -> metrics
	ByDest map[string]*Breakdown // remote IP -> metrics
}

// Breakdown holds metrics for a single app or destination within a time bucket.
type Breakdown struct {
	UploadBytes   int
	DownloadBytes int
	RTTSamples    []float64
	Segments      int
	Retrans       int
}
