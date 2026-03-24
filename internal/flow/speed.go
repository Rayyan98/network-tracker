package flow

import "time"

// speedTracker measures the actual transfer speed of data within a flow,
// as opposed to throughput (total bytes / wall clock second).
//
// It tracks "bursts" — contiguous periods of data transfer — and computes
// bytes/sec for each burst. This tells you how fast data moves when it IS
// moving, regardless of idle gaps between transfers.
//
// A burst ends after burstGap of silence (no packets with payload).
type speedTracker struct {
	burstGap time.Duration

	// Current burst state
	burstStart    time.Time
	burstEnd      time.Time
	burstDownload int
	burstUpload   int
	inBurst       bool

	// Completed burst speeds (bytes/sec) collected since last drain
	downloadSpeeds []float64
	uploadSpeeds   []float64
}

func newSpeedTracker() *speedTracker {
	return &speedTracker{
		burstGap: 200 * time.Millisecond, // 200ms gap = end of burst
	}
}

// RecordData records a data packet in the flow.
func (s *speedTracker) RecordData(ts time.Time, uploadBytes, downloadBytes int) {
	if uploadBytes == 0 && downloadBytes == 0 {
		return
	}

	if !s.inBurst {
		// Start new burst
		s.burstStart = ts
		s.burstEnd = ts
		s.burstDownload = downloadBytes
		s.burstUpload = uploadBytes
		s.inBurst = true
		return
	}

	// Check if this packet continues the current burst or starts a new one
	if ts.Sub(s.burstEnd) > s.burstGap {
		// Finalize previous burst
		s.finalizeBurst()
		// Start new burst
		s.burstStart = ts
		s.burstEnd = ts
		s.burstDownload = downloadBytes
		s.burstUpload = uploadBytes
		return
	}

	// Continue current burst
	s.burstEnd = ts
	s.burstDownload += downloadBytes
	s.burstUpload += uploadBytes
}

func (s *speedTracker) finalizeBurst() {
	duration := s.burstEnd.Sub(s.burstStart)

	// Need at least 10ms of burst to compute meaningful speed
	// (single packet "bursts" don't tell us link speed)
	if duration < 10*time.Millisecond {
		return
	}

	secs := duration.Seconds()
	if s.burstDownload > 0 {
		s.downloadSpeeds = append(s.downloadSpeeds, float64(s.burstDownload)/secs)
	}
	if s.burstUpload > 0 {
		s.uploadSpeeds = append(s.uploadSpeeds, float64(s.burstUpload)/secs)
	}
}

// DrainSpeeds returns accumulated completed burst speeds and resets.
// Does NOT finalize in-progress bursts — those continue accumulating.
func (s *speedTracker) DrainSpeeds() (downloadSpeeds, uploadSpeeds []float64) {
	dl := s.downloadSpeeds
	ul := s.uploadSpeeds
	s.downloadSpeeds = nil
	s.uploadSpeeds = nil
	return dl, ul
}

// FlushBurst finalizes any in-progress burst (called once per second by aggregator).
func (s *speedTracker) FlushBurst() {
	if s.inBurst {
		s.finalizeBurst()
		s.inBurst = false
	}
}
