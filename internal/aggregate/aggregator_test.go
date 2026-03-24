package aggregate

import (
	"testing"
	"time"

	"github.com/rewaa/network-tracker/internal/flow"
)

var noSpeeds = func() ([]float64, []float64) { return nil, nil }

func TestAggregatorBasic(t *testing.T) {
	agg := NewAggregator(func() int { return 5 }, noSpeeds)

	agg.Add(flow.MetricSample{
		Timestamp:     time.Now(),
		UploadBytes:   1000,
		DownloadBytes: 2000,
		RTT:           25 * time.Millisecond,
		IsSegment:     true,
	})
	agg.Add(flow.MetricSample{
		Timestamp:     time.Now(),
		UploadBytes:   500,
		DownloadBytes: 1000,
		RTT:           35 * time.Millisecond,
		IsSegment:     true,
		IsRetrans:     true,
	})

	sample := agg.Flush(time.Now().Unix())

	if sample.UploadBPS != 1500 {
		t.Errorf("expected upload 1500, got %.0f", sample.UploadBPS)
	}
	if sample.DownloadBPS != 3000 {
		t.Errorf("expected download 3000, got %.0f", sample.DownloadBPS)
	}
	if sample.RTT.Count != 2 {
		t.Errorf("expected 2 RTT samples, got %d", sample.RTT.Count)
	}
	if sample.RTT.Avg != 30 {
		t.Errorf("expected avg RTT 30ms, got %.1f", sample.RTT.Avg)
	}
	if sample.RTT.Min != 25 {
		t.Errorf("expected min RTT 25ms, got %.1f", sample.RTT.Min)
	}
	if sample.RTT.Max != 35 {
		t.Errorf("expected max RTT 35ms, got %.1f", sample.RTT.Max)
	}
	if sample.LossPct != 50 {
		t.Errorf("expected 50%% loss, got %.1f%%", sample.LossPct)
	}
}

func TestPercentileBreakdown(t *testing.T) {
	agg := NewAggregator(func() int { return 0 }, noSpeeds)

	// Add 100 RTT samples: 1, 2, 3, ..., 100ms
	for i := 1; i <= 100; i++ {
		agg.Add(flow.MetricSample{
			RTT:       time.Duration(i) * time.Millisecond,
			IsSegment: true,
		})
	}

	sample := agg.Flush(1)

	if sample.RTT.Count != 100 {
		t.Fatalf("expected 100 RTT samples, got %d", sample.RTT.Count)
	}
	if sample.RTT.Min != 1 {
		t.Errorf("expected min=1, got %.1f", sample.RTT.Min)
	}
	if sample.RTT.Max != 100 {
		t.Errorf("expected max=100, got %.1f", sample.RTT.Max)
	}
	// P50 of 1..100 = ~50.5
	if sample.RTT.P50 < 50 || sample.RTT.P50 > 51 {
		t.Errorf("expected p50 ~50.5, got %.1f", sample.RTT.P50)
	}
	// P95 of 1..100 = ~95.05
	if sample.RTT.P95 < 94 || sample.RTT.P95 > 96 {
		t.Errorf("expected p95 ~95, got %.1f", sample.RTT.P95)
	}
	// P99 of 1..100 = ~99.01
	if sample.RTT.P99 < 98 || sample.RTT.P99 > 100 {
		t.Errorf("expected p99 ~99, got %.1f", sample.RTT.P99)
	}
}

func TestJitterCalculation(t *testing.T) {
	agg := NewAggregator(func() int { return 0 }, noSpeeds)
	for _, rtt := range []int{10, 20, 30, 40, 50} {
		agg.Add(flow.MetricSample{RTT: time.Duration(rtt) * time.Millisecond, IsSegment: true})
	}
	sample := agg.Flush(1)
	if sample.JitterMs < 14 || sample.JitterMs > 15 {
		t.Errorf("expected jitter ~14.14ms, got %.2f", sample.JitterMs)
	}
}

func TestUDPTracking(t *testing.T) {
	agg := NewAggregator(func() int { return 0 }, noSpeeds)
	agg.Add(flow.MetricSample{UploadBytes: 500, DownloadBytes: 1000, IsUDP: true})
	agg.Add(flow.MetricSample{UploadBytes: 200, DownloadBytes: 300, IsUDP: false})
	sample := agg.Flush(1)
	if sample.UDPUploadBPS != 500 {
		t.Errorf("expected UDP upload 500, got %.0f", sample.UDPUploadBPS)
	}
	if sample.UploadBPS != 700 {
		t.Errorf("expected total upload 700, got %.0f", sample.UploadBPS)
	}
}

func TestDNSPercentiles(t *testing.T) {
	agg := NewAggregator(func() int { return 0 }, noSpeeds)
	for i := 1; i <= 20; i++ {
		agg.Add(flow.MetricSample{DNSLatency: time.Duration(i) * time.Millisecond})
	}
	sample := agg.Flush(1)
	if sample.DNS.Count != 20 {
		t.Errorf("expected 20 DNS samples, got %d", sample.DNS.Count)
	}
	if sample.DNS.Min != 1 {
		t.Errorf("expected DNS min=1, got %.1f", sample.DNS.Min)
	}
	if sample.DNS.Max != 20 {
		t.Errorf("expected DNS max=20, got %.1f", sample.DNS.Max)
	}
	if sample.DNS.P95 < 18 || sample.DNS.P95 > 20 {
		t.Errorf("expected DNS p95 ~19, got %.1f", sample.DNS.P95)
	}
}

func TestRFactor(t *testing.T) {
	R := computeRFactor(20, 2, 0)
	if R < 85 {
		t.Errorf("expected R>85 for perfect conditions, got %.1f", R)
	}
	R = computeRFactor(68, 10, 0.5)
	if R < 60 || R > 90 {
		t.Errorf("expected R in 60-90 for Karachi typical, got %.1f", R)
	}
	R = computeRFactor(300, 60, 5)
	if R > 65 {
		t.Errorf("expected R<65 for bad conditions, got %.1f", R)
	}
}

func TestQualityScore(t *testing.T) {
	score := computeQualityScore(30*1024*1024, 10*1024*1024, 20, 5, 0.1)
	if score < 85 {
		t.Errorf("expected high quality score, got %d", score)
	}
	score = computeQualityScore(100*1024, 50*1024, 300, 60, 5)
	if score > 40 {
		t.Errorf("expected low quality score, got %d", score)
	}
	score = computeQualityScore(0, 0, 0, 0, 0)
	if score != -1 {
		t.Errorf("expected -1 for no data, got %d", score)
	}
}

func TestUseCaseStatus(t *testing.T) {
	status := computeUseCaseStatus(10*1024*1024, 5*1024*1024, 50, 10, 0.3)
	if status["hd_video_call"] != 2 {
		t.Errorf("expected hd_video_call=good, got %d", status["hd_video_call"])
	}
	if status["audio_call"] != 2 {
		t.Errorf("expected audio_call=good, got %d", status["audio_call"])
	}
	status = computeUseCaseStatus(10*1024*1024, 8*1024*1024, 200, 30, 0.5)
	if status["game_streaming"] == 2 {
		t.Errorf("expected game_streaming != good with 200ms RTT")
	}
	status4k := computeUseCaseStatus(30*1024*1024, 1*1024*1024, 500, 100, 0.5)
	if status4k["4k_streaming"] != 2 {
		t.Errorf("expected 4k_streaming=good with 30MB/s despite high latency, got %d", status4k["4k_streaming"])
	}
}

func TestAggregatorFlushResets(t *testing.T) {
	agg := NewAggregator(func() int { return 0 }, noSpeeds)
	agg.Add(flow.MetricSample{UploadBytes: 100, IsUDP: true, DNSLatency: 10 * time.Millisecond})
	agg.Flush(1)
	sample := agg.Flush(2)
	if sample.UploadBPS != 0 {
		t.Errorf("expected 0 after reset, got %.0f", sample.UploadBPS)
	}
	if sample.DNS.Count != 0 {
		t.Errorf("expected 0 DNS after reset, got %d", sample.DNS.Count)
	}
}

func TestStddev(t *testing.T) {
	vals := []float64{10, 10, 10, 10}
	sd := stddev(vals)
	if sd != 0 {
		t.Errorf("expected stddev=0, got %.2f", sd)
	}
}
