package flow

import "testing"

func TestNoRetransmission(t *testing.T) {
	lt := newLossTracker()

	// Normal sequence: 1000, 1100, 1200
	if lt.RecordOutgoing(1000, 100) {
		t.Error("first segment should not be retransmission")
	}
	if lt.RecordOutgoing(1100, 100) {
		t.Error("second segment should not be retransmission")
	}
	if lt.RecordOutgoing(1200, 100) {
		t.Error("third segment should not be retransmission")
	}

	if lt.TotalSegmentsOut != 3 {
		t.Errorf("expected 3 segments, got %d", lt.TotalSegmentsOut)
	}
	if lt.RetransOut != 0 {
		t.Errorf("expected 0 retransmissions, got %d", lt.RetransOut)
	}
}

func TestRetransmission(t *testing.T) {
	lt := newLossTracker()

	lt.RecordOutgoing(1000, 100) // seq 1000-1100
	lt.RecordOutgoing(1100, 100) // seq 1100-1200
	lt.RecordOutgoing(1000, 100) // retransmission of seq 1000-1100

	if lt.RetransOut != 1 {
		t.Errorf("expected 1 retransmission, got %d", lt.RetransOut)
	}
	if lt.TotalSegmentsOut != 3 {
		t.Errorf("expected 3 total segments, got %d", lt.TotalSegmentsOut)
	}
}

func TestLossRate(t *testing.T) {
	lt := newLossTracker()

	// 10 outgoing segments, 2 retransmissions
	for i := 0; i < 8; i++ {
		lt.RecordOutgoing(uint32(i*100), 100)
	}
	// Retransmit segments 0 and 1
	lt.RecordOutgoing(0, 100)
	lt.RecordOutgoing(100, 100)

	rate := lt.LossRate()
	expected := 2.0 / 10.0
	if rate < expected-0.001 || rate > expected+0.001 {
		t.Errorf("expected loss rate ~%.3f, got %.3f", expected, rate)
	}
}

func TestDuplicateACKs(t *testing.T) {
	lt := newLossTracker()

	lt.RecordACK(1000, false, 0) // first ACK
	lt.RecordACK(1000, false, 0) // dup ACK
	lt.RecordACK(1000, false, 0) // dup ACK
	lt.RecordACK(1100, false, 0) // new ACK

	if lt.DupAcksIn != 2 {
		t.Errorf("expected 2 duplicate ACKs, got %d", lt.DupAcksIn)
	}
}

func TestIncomingRetransmission(t *testing.T) {
	lt := newLossTracker()

	lt.RecordIncoming(5000, 200)
	lt.RecordIncoming(5200, 200)
	lt.RecordIncoming(5000, 200) // retransmission

	if lt.RetransIn != 1 {
		t.Errorf("expected 1 incoming retransmission, got %d", lt.RetransIn)
	}
}

func TestSeqWrapping(t *testing.T) {
	// Test that sequence number comparison handles wrap-around
	if !seqBefore(0xFFFFFFFE, 0x00000002) {
		t.Error("0xFFFFFFFE should be before 0x00000002 (wrap)")
	}
	if !seqAfter(0x00000002, 0xFFFFFFFE) {
		t.Error("0x00000002 should be after 0xFFFFFFFE (wrap)")
	}
}

func TestZeroPayloadIgnored(t *testing.T) {
	lt := newLossTracker()

	// Pure ACKs (payload=0) should not be counted as segments
	if lt.RecordOutgoing(1000, 0) {
		t.Error("zero-payload should not be retransmission")
	}
	if lt.TotalSegmentsOut != 0 {
		t.Errorf("expected 0 segments for zero-payload, got %d", lt.TotalSegmentsOut)
	}
}
