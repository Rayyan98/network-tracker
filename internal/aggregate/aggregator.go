package aggregate

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/rewaa/network-tracker/internal/flow"
)

// Aggregator collects per-packet metrics into 1-second buckets.
type Aggregator struct {
	mu              sync.Mutex
	uploadBytes     int
	downloadBytes   int
	udpUploadBytes  int
	udpDownloadBytes int
	rttSamples      []float64 // in milliseconds
	dnsSamples      []float64 // DNS latency in milliseconds
	segments        int
	retrans         int
	flowCounter     func() int

	// Per-key accumulators
	byApp  map[string]*breakdownAcc
	byDest map[string]*breakdownAcc

	// Rolling window for consistency calculation
	recentDownloads []float64 // last N seconds of download BPS
}

type breakdownAcc struct {
	uploadBytes   int
	downloadBytes int
	rttSamples    []float64
	segments      int
	retrans       int
}

const consistencyWindow = 30 // seconds

func NewAggregator(flowCounter func() int) *Aggregator {
	return &Aggregator{
		flowCounter: flowCounter,
		byApp:       make(map[string]*breakdownAcc),
		byDest:      make(map[string]*breakdownAcc),
	}
}

func getOrCreate(m map[string]*breakdownAcc, key string) *breakdownAcc {
	if b, ok := m[key]; ok {
		return b
	}
	b := &breakdownAcc{}
	m[key] = b
	return b
}

// Add processes a metric sample from the flow tracker.
func (a *Aggregator) Add(s flow.MetricSample) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.uploadBytes += s.UploadBytes
	a.downloadBytes += s.DownloadBytes

	if s.IsUDP {
		a.udpUploadBytes += s.UploadBytes
		a.udpDownloadBytes += s.DownloadBytes
	}

	rttMs := float64(0)
	if s.RTT > 0 {
		rttMs = float64(s.RTT) / float64(time.Millisecond)
		a.rttSamples = append(a.rttSamples, rttMs)
	}

	if s.DNSLatency > 0 {
		dnsMs := float64(s.DNSLatency) / float64(time.Millisecond)
		a.dnsSamples = append(a.dnsSamples, dnsMs)
	}

	if s.IsSegment {
		a.segments++
	}
	if s.IsRetrans {
		a.retrans++
	}

	// Per-destination breakdown
	if s.RemoteIP != "" {
		b := getOrCreate(a.byDest, s.RemoteIP)
		b.uploadBytes += s.UploadBytes
		b.downloadBytes += s.DownloadBytes
		if s.RTT > 0 {
			b.rttSamples = append(b.rttSamples, rttMs)
		}
		if s.IsSegment {
			b.segments++
		}
		if s.IsRetrans {
			b.retrans++
		}
	}

	// Per-app breakdown
	if s.Process != "" {
		b := getOrCreate(a.byApp, s.Process)
		b.uploadBytes += s.UploadBytes
		b.downloadBytes += s.DownloadBytes
		if s.RTT > 0 {
			b.rttSamples = append(b.rttSamples, rttMs)
		}
		if s.IsSegment {
			b.segments++
		}
		if s.IsRetrans {
			b.retrans++
		}
	}
}

// Flush produces an AggregatedSample for the current second and resets accumulators.
func (a *Aggregator) Flush(ts int64) AggregatedSample {
	a.mu.Lock()
	defer a.mu.Unlock()

	sample := AggregatedSample{
		Timestamp:      ts,
		UploadBPS:      float64(a.uploadBytes),
		DownloadBPS:    float64(a.downloadBytes),
		UDPUploadBPS:   float64(a.udpUploadBytes),
		UDPDownloadBPS: float64(a.udpDownloadBytes),
		ActiveFlows:    a.flowCounter(),
		RTTCount:       len(a.rttSamples),
		Segments:       a.segments,
		Retrans:        a.retrans,
		DNSCount:       len(a.dnsSamples),
	}

	if len(a.rttSamples) > 0 {
		sort.Float64s(a.rttSamples)
		sample.RTTAvgMs = avg(a.rttSamples)
		sample.RTTP95Ms = percentile(a.rttSamples, 0.95)
		sample.RTTMaxMs = a.rttSamples[len(a.rttSamples)-1]
		sample.JitterMs = stddev(a.rttSamples)
	}

	if a.segments > 0 {
		sample.LossPct = float64(a.retrans) / float64(a.segments) * 100
	}

	if len(a.dnsSamples) > 0 {
		sort.Float64s(a.dnsSamples)
		sample.DNSAvgMs = avg(a.dnsSamples)
		sample.DNSMaxMs = a.dnsSamples[len(a.dnsSamples)-1]
	}

	// Update rolling download window for consistency
	a.recentDownloads = append(a.recentDownloads, sample.DownloadBPS)
	if len(a.recentDownloads) > consistencyWindow {
		a.recentDownloads = a.recentDownloads[len(a.recentDownloads)-consistencyWindow:]
	}

	// Compute quality score
	consistency := computeConsistency(a.recentDownloads)
	sample.QualityScore = computeQualityScore(sample.DownloadBPS, sample.UploadBPS,
		sample.RTTAvgMs, sample.JitterMs, sample.LossPct, consistency)
	sample.UseCaseStatus = computeUseCaseStatus(sample.DownloadBPS, sample.UploadBPS,
		sample.RTTAvgMs, sample.JitterMs, sample.LossPct)

	// Build breakdowns
	sample.ByApp = buildBreakdowns(a.byApp)
	sample.ByDest = buildBreakdowns(a.byDest)

	// Reset
	a.uploadBytes = 0
	a.downloadBytes = 0
	a.udpUploadBytes = 0
	a.udpDownloadBytes = 0
	a.rttSamples = a.rttSamples[:0]
	a.dnsSamples = a.dnsSamples[:0]
	a.segments = 0
	a.retrans = 0
	a.byApp = make(map[string]*breakdownAcc)
	a.byDest = make(map[string]*breakdownAcc)

	return sample
}

func buildBreakdowns(accs map[string]*breakdownAcc) map[string]*Breakdown {
	if len(accs) == 0 {
		return nil
	}
	result := make(map[string]*Breakdown, len(accs))
	for key, acc := range accs {
		result[key] = &Breakdown{
			UploadBytes:   acc.uploadBytes,
			DownloadBytes: acc.downloadBytes,
			RTTSamples:    acc.rttSamples,
			Segments:      acc.segments,
			Retrans:       acc.retrans,
		}
	}
	return result
}

// Run ticks every second, flushing accumulated metrics to the output channel.
func (a *Aggregator) Run(out chan<- AggregatedSample, done <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case t := <-ticker.C:
			ts := t.Unix()
			sample := a.Flush(ts)
			select {
			case out <- sample:
			case <-done:
				return
			}
		case <-done:
			return
		}
	}
}

// --- Quality Score ---

// computeConsistency returns 0.0 (unstable) to 1.0 (perfectly stable).
// Based on coefficient of variation of recent download throughput.
func computeConsistency(downloads []float64) float64 {
	if len(downloads) < 5 {
		return 1.0 // not enough data, assume good
	}
	mean := avg(downloads)
	if mean < 1000 { // less than 1KB/s = essentially idle
		return 1.0
	}
	sd := stddev(downloads)
	cv := sd / mean // coefficient of variation
	// cv=0 -> perfect, cv>=1 -> very unstable
	score := 1.0 - math.Min(cv, 1.0)
	return score
}

func computeQualityScore(downBPS, upBPS, rttMs, jitterMs, lossPct, consistency float64) int {
	// Each component scores 0-100, then weighted average
	dlScore := scoreThreshold(downBPS, 25*1024*1024, 5*1024*1024, 1*1024*1024) // 25MB, 5MB, 1MB
	ulScore := scoreThreshold(upBPS, 10*1024*1024, 2*1024*1024, 512*1024)      // 10MB, 2MB, 512KB
	rttScore := scoreThresholdInverse(rttMs, 30, 100, 200)                       // <30=100, >200=0
	jitterScore := scoreThresholdInverse(jitterMs, 10, 30, 50)                   // <10=100, >50=0
	lossScore := scoreThresholdInverse(lossPct, 0.1, 1.0, 5.0)                  // <0.1%=100, >5%=0
	consistScore := consistency * 100

	// If no traffic, don't penalize
	if downBPS < 100 && upBPS < 100 {
		// Idle - only score what we can measure (RTT, loss from whatever data we have)
		if rttMs == 0 {
			return -1 // no data at all
		}
		return clampScore(rttScore*0.4 + jitterScore*0.3 + lossScore*0.3)
	}

	score := dlScore*0.20 + ulScore*0.15 + rttScore*0.20 + jitterScore*0.20 + lossScore*0.15 + consistScore*0.10
	return clampScore(score)
}

// scoreThreshold: higher value = better. Returns 0-100.
func scoreThreshold(val, great, ok, bad float64) float64 {
	if val >= great {
		return 100
	}
	if val >= ok {
		return 50 + 50*(val-ok)/(great-ok)
	}
	if val >= bad {
		return 50 * (val - bad) / (ok - bad)
	}
	return 0
}

// scoreThresholdInverse: lower value = better. Returns 0-100.
func scoreThresholdInverse(val, great, ok, bad float64) float64 {
	if val <= great {
		return 100
	}
	if val <= ok {
		return 50 + 50*(ok-val)/(ok-great)
	}
	if val <= bad {
		return 50 * (bad - val) / (bad - ok)
	}
	return 0
}

func clampScore(s float64) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return int(math.Round(s))
}

// --- Use Case Status ---
// 0 = not ready, 1 = degraded, 2 = good

func computeUseCaseStatus(downBPS, upBPS, rttMs, jitterMs, lossPct float64) map[string]int {
	return map[string]int{
		"hd_video_call":  useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct, 3*1024*1024, 3*1024*1024, 100, 20, 0.5),
		"audio_call":     useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct, 100*1024, 100*1024, 150, 30, 1.0),
		"screen_sharing": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct, 1*1024*1024, 2*1024*1024, 200, 50, 2.0),
		"game_streaming": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct, 5*1024*1024, 5*1024*1024, 50, 15, 1.0),
		"4k_streaming":   useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct, 25*1024*1024, 0, 500, 100, 5.0),
	}
}

func useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct float64,
	needDown, needUp, maxRTT, maxJitter, maxLoss float64) int {

	good := true
	degraded := false

	if needDown > 0 && downBPS < needDown {
		if downBPS >= needDown*0.5 {
			degraded = true
		} else {
			good = false
		}
	}
	if needUp > 0 && upBPS < needUp {
		if upBPS >= needUp*0.5 {
			degraded = true
		} else {
			good = false
		}
	}
	if rttMs > 0 && rttMs > maxRTT {
		if rttMs <= maxRTT*1.5 {
			degraded = true
		} else {
			good = false
		}
	}
	if jitterMs > 0 && jitterMs > maxJitter {
		if jitterMs <= maxJitter*2 {
			degraded = true
		} else {
			good = false
		}
	}
	if lossPct > maxLoss {
		if lossPct <= maxLoss*3 {
			degraded = true
		} else {
			good = false
		}
	}

	if !good {
		return 0
	}
	if degraded {
		return 1
	}
	return 2
}

// --- Math helpers ---

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func stddev(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	mean := avg(vals)
	sumSq := 0.0
	for _, v := range vals {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
