package aggregate

// Percentiles holds the full percentile breakdown for a metric.
type Percentiles struct {
	Min   float64
	Avg   float64
	P50   float64
	P90   float64
	P95   float64
	P99   float64
	Max   float64
	Count int
}

// AggregatedSample represents metrics for a single time bucket.
type AggregatedSample struct {
	Timestamp   int64
	UploadBPS   float64
	DownloadBPS float64

	// UDP subset of throughput
	UDPUploadBPS   float64
	UDPDownloadBPS float64

	// Transfer speed (link capacity from burst measurement)
	TransferDownBPS float64
	TransferUpBPS   float64

	// RTT percentiles (from N RTT samples in this second)
	RTT Percentiles

	// Jitter (stddev of RTT samples — single value per second)
	JitterMs float64

	// Packet loss
	LossPct  float64
	Segments int
	Retrans  int

	// DNS percentiles (from N DNS samples in this second)
	DNS Percentiles

	// Quality
	QualityScore  int
	UseCaseStatus map[string]int
	ActiveFlows   int

	// Per-key breakdowns
	ByApp  map[string]*Breakdown
	ByDest map[string]*Breakdown
}

// Breakdown holds metrics for a single app or destination within a time bucket.
type Breakdown struct {
	UploadBytes   int
	DownloadBytes int
	RTTSamples    []float64
	Segments      int
	Retrans       int
}
