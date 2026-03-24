package aggregate

import (
	"testing"
	"time"

	"github.com/rewaa/network-tracker/internal/flow"
)

func TestAggregatorBasic(t *testing.T) {
	agg := NewAggregator(func() int { return 5 })

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
	if sample.RTTCount != 2 {
		t.Errorf("expected 2 RTT samples, got %d", sample.RTTCount)
	}
	if sample.RTTAvgMs != 30 {
		t.Errorf("expected avg RTT 30ms, got %.1f", sample.RTTAvgMs)
	}
	if sample.Segments != 2 {
		t.Errorf("expected 2 segments, got %d", sample.Segments)
	}
	if sample.Retrans != 1 {
		t.Errorf("expected 1 retransmission, got %d", sample.Retrans)
	}
	if sample.LossPct != 50 {
		t.Errorf("expected 50%% loss, got %.1f%%", sample.LossPct)
	}
	if sample.ActiveFlows != 5 {
		t.Errorf("expected 5 active flows, got %d", sample.ActiveFlows)
	}
}

func TestJitterCalculation(t *testing.T) {
	agg := NewAggregator(func() int { return 0 })

	// Add samples with known RTTs: 10, 20, 30, 40, 50ms
	for _, rtt := range []int{10, 20, 30, 40, 50} {
		agg.Add(flow.MetricSample{
			RTT:       time.Duration(rtt) * time.Millisecond,
			IsSegment: true,
		})
	}

	sample := agg.Flush(1)

	// Mean = 30, stddev = sqrt((400+100+0+100+400)/5) = sqrt(200) ~= 14.14
	if sample.JitterMs < 14 || sample.JitterMs > 15 {
		t.Errorf("expected jitter ~14.14ms, got %.2f", sample.JitterMs)
	}
}

func TestUDPTracking(t *testing.T) {
	agg := NewAggregator(func() int { return 0 })

	agg.Add(flow.MetricSample{
		UploadBytes:   500,
		DownloadBytes: 1000,
		IsUDP:         true,
	})

	agg.Add(flow.MetricSample{
		UploadBytes:   200,
		DownloadBytes: 300,
		IsUDP:         false,
	})

	sample := agg.Flush(1)

	if sample.UDPUploadBPS != 500 {
		t.Errorf("expected UDP upload 500, got %.0f", sample.UDPUploadBPS)
	}
	if sample.UDPDownloadBPS != 1000 {
		t.Errorf("expected UDP download 1000, got %.0f", sample.UDPDownloadBPS)
	}
	if sample.UploadBPS != 700 {
		t.Errorf("expected total upload 700, got %.0f", sample.UploadBPS)
	}
}

func TestDNSTracking(t *testing.T) {
	agg := NewAggregator(func() int { return 0 })

	agg.Add(flow.MetricSample{DNSLatency: 15 * time.Millisecond})
	agg.Add(flow.MetricSample{DNSLatency: 25 * time.Millisecond})

	sample := agg.Flush(1)

	if sample.DNSCount != 2 {
		t.Errorf("expected 2 DNS samples, got %d", sample.DNSCount)
	}
	if sample.DNSAvgMs != 20 {
		t.Errorf("expected DNS avg 20ms, got %.1f", sample.DNSAvgMs)
	}
	if sample.DNSMaxMs != 25 {
		t.Errorf("expected DNS max 25ms, got %.1f", sample.DNSMaxMs)
	}
}

func TestRFactor(t *testing.T) {
	// Perfect conditions: low latency, no jitter, no loss
	R := computeRFactor(20, 2, 0)
	if R < 85 {
		t.Errorf("expected R>85 for perfect conditions, got %.1f", R)
	}

	// Karachi typical: 68ms RTT, 10ms jitter, 0.5% loss
	R = computeRFactor(68, 10, 0.5)
	if R < 60 || R > 90 {
		t.Errorf("expected R in 60-90 for Karachi typical, got %.1f", R)
	}

	// Bad conditions: 300ms RTT, 60ms jitter, 5% loss
	R = computeRFactor(300, 60, 5)
	if R > 65 {
		t.Errorf("expected R<65 for bad conditions, got %.1f", R)
	}
}

func TestRFactorToMOS(t *testing.T) {
	// R=93 should give MOS ~4.3
	mos := rFactorToMOS(93)
	if mos < 4.2 || mos > 4.4 {
		t.Errorf("expected MOS ~4.3 for R=93, got %.2f", mos)
	}

	// R=50 should give MOS ~2.6
	mos = rFactorToMOS(50)
	if mos < 2.4 || mos > 2.9 {
		t.Errorf("expected MOS ~2.6 for R=50, got %.2f", mos)
	}
}

func TestQualityScore(t *testing.T) {
	// Perfect conditions
	score := computeQualityScore(
		30*1024*1024, // 30MB/s down
		10*1024*1024, // 10MB/s up
		20,           // 20ms RTT
		5,            // 5ms jitter
		0.1,          // 0.1% loss
	)
	if score < 85 {
		t.Errorf("expected high quality score for perfect conditions, got %d", score)
	}

	// Terrible conditions
	score = computeQualityScore(
		100*1024, // 100KB/s down
		50*1024,  // 50KB/s up
		300,      // 300ms RTT
		60,       // 60ms jitter
		5,        // 5% loss
	)
	if score > 40 {
		t.Errorf("expected low quality score for terrible conditions, got %d", score)
	}

	// Idle with no data
	score = computeQualityScore(0, 0, 0, 0, 0)
	if score != -1 {
		t.Errorf("expected -1 for no data, got %d", score)
	}
}

func TestUseCaseStatus(t *testing.T) {
	// Good conditions for HD video call
	status := computeUseCaseStatus(
		10*1024*1024, // 10MB/s down
		5*1024*1024,  // 5MB/s up
		50,           // 50ms RTT
		10,           // 10ms jitter
		0.3,          // 0.3% loss
	)

	if status["hd_video_call"] != 2 {
		t.Errorf("expected hd_video_call=good(2), got %d", status["hd_video_call"])
	}
	if status["audio_call"] != 2 {
		t.Errorf("expected audio_call=good(2), got %d", status["audio_call"])
	}

	// Bad conditions for game streaming (high latency)
	status = computeUseCaseStatus(
		10*1024*1024,
		8*1024*1024,
		200, // too high for gaming (limit is 100ms)
		30,
		0.5,
	)

	if status["game_streaming"] == 2 {
		t.Errorf("expected game_streaming != good with 200ms RTT, got %d", status["game_streaming"])
	}

	// 4K streaming: 10 MB/s is below the 25 MB/s requirement, so should be degraded (not good)
	if status["4k_streaming"] == 2 {
		t.Errorf("expected 4k_streaming not good with 10MB/s (needs 25MB/s), got %d", status["4k_streaming"])
	}

	// 4K with enough bandwidth but high latency should still work (latency irrelevant)
	status4k := computeUseCaseStatus(30*1024*1024, 1*1024*1024, 500, 100, 0.5)
	if status4k["4k_streaming"] != 2 {
		t.Errorf("expected 4k_streaming=good with 30MB/s despite high latency, got %d", status4k["4k_streaming"])
	}
}

func TestAggregatorFlushResets(t *testing.T) {
	agg := NewAggregator(func() int { return 0 })

	agg.Add(flow.MetricSample{UploadBytes: 100, IsUDP: true, DNSLatency: 10 * time.Millisecond})
	agg.Flush(1)

	sample := agg.Flush(2)
	if sample.UploadBPS != 0 {
		t.Errorf("expected 0 after reset, got %.0f", sample.UploadBPS)
	}
	if sample.UDPUploadBPS != 0 {
		t.Errorf("expected UDP 0 after reset, got %.0f", sample.UDPUploadBPS)
	}
	if sample.DNSCount != 0 {
		t.Errorf("expected 0 DNS after reset, got %d", sample.DNSCount)
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p95 := percentile(vals, 0.95)
	if p95 < 9.5 || p95 > 9.6 {
		t.Errorf("expected p95 ~9.55, got %.2f", p95)
	}
}

func TestStddev(t *testing.T) {
	vals := []float64{10, 10, 10, 10}
	sd := stddev(vals)
	if sd != 0 {
		t.Errorf("expected stddev=0 for constant values, got %.2f", sd)
	}
}
