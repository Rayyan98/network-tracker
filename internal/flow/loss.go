package flow

type lossTracker struct {
	// Per-direction highest sequence number seen
	highestSeqOut uint32 // local -> remote
	highestSeqIn  uint32 // remote -> local
	seenOut       bool
	seenIn        bool

	// Counters
	TotalSegmentsOut uint64
	TotalSegmentsIn  uint64
	RetransOut       uint64
	RetransIn        uint64

	// Duplicate ACK tracking
	lastAckOut uint32 // last ACK number from local
	lastAckIn  uint32 // last ACK number from remote
	DupAcksOut uint64
	DupAcksIn  uint64
}

func newLossTracker() *lossTracker {
	return &lossTracker{}
}

// RecordOutgoing processes a data segment sent from local to remote.
// Returns true if this is a retransmission.
func (l *lossTracker) RecordOutgoing(seq uint32, payloadLen int) bool {
	if payloadLen == 0 {
		return false
	}
	l.TotalSegmentsOut++

	endSeq := seq + uint32(payloadLen)
	if !l.seenOut {
		l.highestSeqOut = endSeq
		l.seenOut = true
		return false
	}

	// Retransmission: segment's data range is entirely below highest seen
	if seqBefore(endSeq, l.highestSeqOut) || endSeq == l.highestSeqOut {
		if seq != l.highestSeqOut { // not just the next segment
			l.RetransOut++
			return true
		}
	}

	if seqAfter(endSeq, l.highestSeqOut) {
		l.highestSeqOut = endSeq
	}
	return false
}

// RecordIncoming processes a data segment received from remote to local.
// Returns true if this is a retransmission.
func (l *lossTracker) RecordIncoming(seq uint32, payloadLen int) bool {
	if payloadLen == 0 {
		return false
	}
	l.TotalSegmentsIn++

	endSeq := seq + uint32(payloadLen)
	if !l.seenIn {
		l.highestSeqIn = endSeq
		l.seenIn = true
		return false
	}

	if seqBefore(endSeq, l.highestSeqIn) || endSeq == l.highestSeqIn {
		if seq != l.highestSeqIn {
			l.RetransIn++
			return true
		}
	}

	if seqAfter(endSeq, l.highestSeqIn) {
		l.highestSeqIn = endSeq
	}
	return false
}

// RecordACK tracks duplicate ACKs.
func (l *lossTracker) RecordACK(ackNum uint32, fromLocal bool, payloadLen int) {
	if payloadLen > 0 {
		return // not a pure ACK
	}
	if fromLocal {
		if ackNum == l.lastAckOut && l.lastAckOut != 0 {
			l.DupAcksOut++
		}
		l.lastAckOut = ackNum
	} else {
		if ackNum == l.lastAckIn && l.lastAckIn != 0 {
			l.DupAcksIn++
		}
		l.lastAckIn = ackNum
	}
}

// LossRate returns the estimated packet loss rate (0.0 to 1.0).
func (l *lossTracker) LossRate() float64 {
	total := l.TotalSegmentsOut + l.TotalSegmentsIn
	retrans := l.RetransOut + l.RetransIn
	if total == 0 {
		return 0
	}
	return float64(retrans) / float64(total)
}

// seqBefore returns true if a is before b in TCP sequence space (handles wrapping).
func seqBefore(a, b uint32) bool {
	return int32(a-b) < 0
}

func seqAfter(a, b uint32) bool {
	return int32(a-b) > 0
}
