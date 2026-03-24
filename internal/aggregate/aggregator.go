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

	// Transfer speed burst samples (bytes/sec)
	dlSpeeds []float64
	ulSpeeds []float64

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

	// Collect burst transfer speeds
	a.dlSpeeds = append(a.dlSpeeds, s.DownloadSpeeds...)
	a.ulSpeeds = append(a.ulSpeeds, s.UploadSpeeds...)

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

	// Transfer speed: p90 of burst speeds (what your link actually delivers)
	if len(a.dlSpeeds) > 0 {
		sort.Float64s(a.dlSpeeds)
		sample.TransferDownBPS = percentile(a.dlSpeeds, 0.90)
		sample.TransferCount += len(a.dlSpeeds)
	}
	if len(a.ulSpeeds) > 0 {
		sort.Float64s(a.ulSpeeds)
		sample.TransferUpBPS = percentile(a.ulSpeeds, 0.90)
		sample.TransferCount += len(a.ulSpeeds)
	}

	// Quality score: use transfer speed for throughput component (not raw throughput)
	// This way idle periods with good burst speed don't tank the score
	dlForScore := sample.TransferDownBPS
	ulForScore := sample.TransferUpBPS
	if dlForScore == 0 {
		dlForScore = sample.DownloadBPS // fallback to throughput if no bursts
	}
	if ulForScore == 0 {
		ulForScore = sample.UploadBPS
	}
	sample.QualityScore = computeQualityScore(dlForScore, ulForScore,
		sample.RTTAvgMs, sample.JitterMs, sample.LossPct)
	sample.UseCaseStatus = computeUseCaseStatus(dlForScore, ulForScore,
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
	a.dlSpeeds = a.dlSpeeds[:0]
	a.ulSpeeds = a.ulSpeeds[:0]
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

// --- Quality Score: ITU-T G.107 E-Model ---
//
// The E-Model produces an R-factor (0-100) based on latency, jitter, and packet loss.
// This is the industry standard used by telecom, Grafana VoIP dashboards, etc.
//
// R = 93.2 - Id - Ie
//   Id = delay impairment = 0.024*d + 0.11*(d - 177.3)*H(d - 177.3)
//     where d = one-way delay (RTT/2 + jitter buffer), H = Heaviside step
//   Ie = equipment impairment from packet loss (codec-dependent)
//     Ie = 0 + 30 * ln(1 + 15*e)  where e = packet loss fraction
//
// We then blend R with throughput adequacy to get a combined network quality score.
//
// R-factor interpretation:
//   90-100 = Excellent (MOS 4.3+)
//   80-90  = Good (MOS 4.0-4.3)
//   70-80  = Fair (MOS 3.6-4.0)
//   60-70  = Poor (MOS 3.1-3.6)
//   <60    = Bad  (MOS <3.1)

func computeRFactor(rttMs, jitterMs, lossPct float64) float64 {
	if rttMs == 0 && lossPct == 0 {
		return -1 // no data
	}

	// One-way delay: RTT/2 + jitter buffer (typically 2x jitter)
	oneWayDelay := rttMs/2.0 + jitterMs*2.0

	// Delay impairment (Id) — simplified G.107
	Id := 0.024*oneWayDelay + 0.11*math.Max(0, oneWayDelay-177.3)

	// Equipment/loss impairment (Ie) — G.113 annex for wideband codec
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

// rFactorToMOS converts R-factor to MOS (1-5) per ITU-T G.107 Annex B.
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
	// R-factor from ITU E-Model (network quality: latency + jitter + loss)
	R := computeRFactor(rttMs, jitterMs, lossPct)

	// If no RTT data at all and no traffic, we have nothing to score
	if R < 0 && downBPS < 100 && upBPS < 100 {
		return -1
	}

	// If we have RTT data but no throughput, score purely on R-factor
	if downBPS < 100 && upBPS < 100 {
		if R < 0 {
			return -1
		}
		return clampScore(R)
	}

	// Throughput adequacy (0-100) — based on real-world recommendations
	// 10-25 Mbps down and 5 Mbps up for good interactive experience
	dlScore := scoreThreshold(downBPS, 25*1024*1024, 10*1024*1024, 3*1024*1024)
	ulScore := scoreThreshold(upBPS, 5*1024*1024, 2*1024*1024, 500*1024)
	throughputScore := dlScore*0.6 + ulScore*0.4

	// If we have R-factor, blend 60% R-factor + 40% throughput
	// R-factor covers the real-time quality; throughput covers bandwidth adequacy
	if R >= 0 {
		return clampScore(R*0.6 + throughputScore*0.4)
	}

	// No R-factor data but have throughput — score on throughput alone
	return clampScore(throughputScore)
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
//
// Thresholds from official platform docs + ITU standards:
//   Latency: ITU-T G.114 — 150ms one-way = 300ms RTT max acceptable
//   Jitter: ITU VoIP — <20ms good, >50ms bad
//   Loss: ITU VoIP — <1% acceptable, >2.5% degraded
//   Bandwidth: Official Zoom/Meet/Discord docs + real-world recommendations

func computeUseCaseStatus(downBPS, upBPS, rttMs, jitterMs, lossPct float64) map[string]int {
	return map[string]int{
		// Google Meet: 3.2 Mbps out, <50ms to 8.8.8.8, up to 3.6 Mbps for 1080p
		"hd_video_call": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			3.6*1024*1024, 3.2*1024*1024, 150, 30, 1.0),
		// VoIP standard: 100 Kbps, ITU G.114 <150ms one-way
		"audio_call": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			100*1024, 100*1024, 300, 50, 2.0),
		// Screen sharing: 2-4 Mbps up, latency less critical
		"screen_sharing": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			2*1024*1024, 3*1024*1024, 200, 50, 2.0),
		// Game streaming to Discord: needs consistent low latency + good upload
		"game_streaming": useCaseCheck(downBPS, upBPS, rttMs, jitterMs, lossPct,
			5*1024*1024, 6*1024*1024, 100, 20, 1.0),
		// 4K streaming: Netflix/YouTube recommend 25 Mbps, latency irrelevant (buffered)
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
