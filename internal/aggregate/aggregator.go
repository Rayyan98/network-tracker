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
	mu               sync.Mutex
	uploadBytes      int
	downloadBytes    int
	udpUploadBytes   int
	udpDownloadBytes int
	rttSamples       []float64 // in milliseconds
	dnsSamples       []float64 // DNS latency in milliseconds
	segments         int
	retrans          int
	flowCounter      func() int

	// Flow tracker reference for draining burst speeds
	speedDrainer func() ([]float64, []float64)

	// Per-key accumulators
	byApp  map[string]*breakdownAcc
	byDest map[string]*breakdownAcc
}

type breakdownAcc struct {
	uploadBytes   int
	downloadBytes int
	rttSamples    []float64
	segments      int
	retrans       int
}

func NewAggregator(flowCounter func() int, speedDrainer func() ([]float64, []float64)) *Aggregator {
	return &Aggregator{
		flowCounter:  flowCounter,
		speedDrainer: speedDrainer,
		byApp:        make(map[string]*breakdownAcc),
		byDest:       make(map[string]*breakdownAcc),
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
		Segments:       a.segments,
		Retrans:        a.retrans,
	}

	// RTT percentiles
	sample.RTT = computePercentiles(a.rttSamples)

	// Jitter (stddev of RTT samples)
	if len(a.rttSamples) >= 2 {
		sample.JitterMs = stddev(a.rttSamples)
	}

	// Packet loss
	if a.segments > 0 {
		sample.LossPct = float64(a.retrans) / float64(a.segments) * 100
	}

	// DNS percentiles
	sample.DNS = computePercentiles(a.dnsSamples)

	// Transfer speed: drain burst speeds from all flows, compute p90
	dlSpeeds, ulSpeeds := a.speedDrainer()
	if len(dlSpeeds) > 0 {
		sort.Float64s(dlSpeeds)
		sample.TransferDownBPS = percentile(dlSpeeds, 0.90)
	}
	if len(ulSpeeds) > 0 {
		sort.Float64s(ulSpeeds)
		sample.TransferUpBPS = percentile(ulSpeeds, 0.90)
	}

	// Quality score: use transfer speed for throughput component
	dlForScore := sample.TransferDownBPS
	ulForScore := sample.TransferUpBPS
	if dlForScore == 0 {
		dlForScore = sample.DownloadBPS
	}
	if ulForScore == 0 {
		ulForScore = sample.UploadBPS
	}
	sample.QualityScore = computeQualityScore(dlForScore, ulForScore,
		sample.RTT.Avg, sample.JitterMs, sample.LossPct)
	sample.UseCaseStatus = computeUseCaseStatus(dlForScore, ulForScore,
		sample.RTT.Avg, sample.JitterMs, sample.LossPct)

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

// computePercentiles computes full percentile breakdown from a slice of samples.
func computePercentiles(vals []float64) Percentiles {
	if len(vals) == 0 {
		return Percentiles{}
	}
	sort.Float64s(vals)
	return Percentiles{
		Min:   vals[0],
		Avg:   avg(vals),
		P50:   percentile(vals, 0.50),
		P90:   percentile(vals, 0.90),
		P95:   percentile(vals, 0.95),
		P99:   percentile(vals, 0.99),
		Max:   vals[len(vals)-1],
		Count: len(vals),
	}
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

// --- Quality Score: ITU-T G.107 E-Model ---

func computeRFactor(rttMs, jitterMs, lossPct float64) float64 {
	if rttMs == 0 && lossPct == 0 {
		return -1
	}
	oneWayDelay := rttMs/2.0 + jitterMs*2.0
	Id := 0.024*oneWayDelay + 0.11*math.Max(0, oneWayDelay-177.3)
	lossFrac := lossPct / 100.0
	Ie := 0.0
	if lossFrac > 0 {
		Ie = 30.0 * math.Log(1.0+15.0*lossFrac)
	}
	R := 93.2 - Id - Ie
	if R < 0 {
		R = 0
	}
	if R > 100 {
		R = 100
	}
	return R
}

func rFactorToMOS(R float64) float64 {
	if R < 0 {
		return 1
	}
	if R > 100 {
		return 4.5
	}
	return 1.0 + 0.035*R + R*(R-60.0)*(100.0-R)*0.0000007
}

func computeQualityScore(downBPS, upBPS, rttMs, jitterMs, lossPct float64) int {
	R := computeRFactor(rttMs, jitterMs, lossPct)
	if R < 0 && downBPS < 100 && upBPS < 100 {
		return -1
	}
	if downBPS < 100 && upBPS < 100 {
		if R < 0 {
			return -1
		}
		return clampScore(R)
	}
	dlScore := scoreThreshold(downBPS, 25*1024*1024, 10*1024*1024, 3*1024*1024)
	ulScore := scoreThreshold(upBPS, 5*1024*1024, 2*1024*1024, 500*1024)
	throughputScore := dlScore*0.6 + ulScore*0.4
	if R >= 0 {
		return clampScore(R*0.6 + throughputScore*0.4)
	}
	return clampScore(throughputScore)
}

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

func computeUseCaseStatus(downBPS, upBPS, rttMs, jitterMs, lossPct float64) map[string]int {
	return map[string]int{
		"hd_video_call": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			3.6*1024*1024, 3.2*1024*1024, 150, 30, 1.0),
		"audio_call": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			100*1024, 100*1024, 300, 50, 2.0),
		"screen_sharing": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			2*1024*1024, 3*1024*1024, 200, 50, 2.0),
		"game_streaming": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			5*1024*1024, 6*1024*1024, 100, 20, 1.0),
		"4k_streaming": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			25*1024*1024, 0, 0, 0, 5.0),
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
	if maxRTT > 0 && rttMs > 0 && rttMs > maxRTT {
		if rttMs <= maxRTT*1.5 {
			degraded = true
		} else {
			good = false
		}
	}
	if maxJitter > 0 && jitterMs > 0 && jitterMs > maxJitter {
		if jitterMs <= maxJitter*2 {
			degraded = true
		} else {
			good = false
		}
	}
	if maxLoss > 0 && lossPct > maxLoss {
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
