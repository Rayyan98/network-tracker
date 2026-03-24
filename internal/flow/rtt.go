package flow

import "time"

const (
	maxUnackedEntries = 100
	ewmaAlpha         = 0.125
)

type rttTracker struct {
	// Handshake RTT
	synTime      time.Time
	handshakeRTT time.Duration

	// Data/ACK RTT - tracks outgoing sequence numbers and their send times
	unacked map[uint32]time.Time

	// Smoothed RTT (EWMA)
	srtt    time.Duration
	hasSRTT bool

	// Track retransmitted seqs to apply Karn's algorithm
	retransmittedSeqs map[uint32]bool
}

func newRTTTracker() *rttTracker {
	return &rttTracker{
		unacked:           make(map[uint32]time.Time),
		retransmittedSeqs: make(map[uint32]bool),
	}
}

// RecordSYN records the time a SYN was sent from the local side.
func (r *rttTracker) RecordSYN(ts time.Time) {
	r.synTime = ts
}

// RecordSYNACK computes handshake RTT from a received SYN-ACK.
func (r *rttTracker) RecordSYNACK(ts time.Time) time.Duration {
	if r.synTime.IsZero() {
		return 0
	}
	rtt := ts.Sub(r.synTime)
	r.handshakeRTT = rtt
	r.updateSRTT(rtt)
	return rtt
}

// RecordSend records an outgoing data segment's sequence number.
func (r *rttTracker) RecordSend(seq uint32, ts time.Time) {
	if len(r.unacked) >= maxUnackedEntries {
		// Evict oldest
		var oldestSeq uint32
		var oldestTime time.Time
		for s, t := range r.unacked {
			if oldestTime.IsZero() || t.Before(oldestTime) {
				oldestSeq = s
				oldestTime = t
			}
		}
		delete(r.unacked, oldestSeq)
	}
	r.unacked[seq] = ts
}

// RecordRetransmit marks a sequence as retransmitted (Karn's algorithm: skip RTT sample).
func (r *rttTracker) RecordRetransmit(seq uint32) {
	r.retransmittedSeqs[seq] = true
}

// RecordACK processes an incoming ACK and returns the RTT sample if valid.
func (r *rttTracker) RecordACK(ackNum uint32, ts time.Time) time.Duration {
	// Find the highest sequence number that this ACK covers
	var bestSeq uint32
	var bestTime time.Time
	found := false

	for seq, sendTime := range r.unacked {
		if seq < ackNum {
			if !found || seq > bestSeq {
				bestSeq = seq
				bestTime = sendTime
				found = true
			}
		}
	}

	if !found {
		return 0
	}

	// Clean up all acked sequences
	for seq := range r.unacked {
		if seq < ackNum {
			delete(r.unacked, seq)
		}
	}

	// Karn's algorithm: don't use RTT sample if the segment was retransmitted
	if r.retransmittedSeqs[bestSeq] {
		delete(r.retransmittedSeqs, bestSeq)
		return 0
	}
	delete(r.retransmittedSeqs, bestSeq)

	rtt := ts.Sub(bestTime)
	if rtt > 0 {
		r.updateSRTT(rtt)
	}
	return rtt
}

func (r *rttTracker) updateSRTT(rtt time.Duration) {
	if !r.hasSRTT {
		r.srtt = rtt
		r.hasSRTT = true
		return
	}
	// EWMA: srtt = (1 - alpha) * srtt + alpha * rtt
	r.srtt = time.Duration(float64(r.srtt)*(1-ewmaAlpha) + float64(rtt)*ewmaAlpha)
}

// SRTT returns the smoothed RTT, or 0 if no samples yet.
func (r *rttTracker) SRTT() time.Duration {
	return r.srtt
}
