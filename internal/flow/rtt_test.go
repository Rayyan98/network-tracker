package flow

import (
	"testing"
	"time"
)

func TestHandshakeRTT(t *testing.T) {
	rt := newRTTTracker()

	synTime := time.Now()
	rt.RecordSYN(synTime)

	synAckTime := synTime.Add(25 * time.Millisecond)
	rtt := rt.RecordSYNACK(synAckTime)

	if rtt != 25*time.Millisecond {
		t.Errorf("expected handshake RTT of 25ms, got %v", rtt)
	}

	if rt.SRTT() != 25*time.Millisecond {
		t.Errorf("expected SRTT of 25ms, got %v", rt.SRTT())
	}
}

func TestDataACKRTT(t *testing.T) {
	rt := newRTTTracker()

	t0 := time.Now()
	rt.RecordSend(1000, t0)

	// ACK acknowledging seq 1000 (payload was 100 bytes, so ack=1100)
	rtt := rt.RecordACK(1100, t0.Add(30*time.Millisecond))

	if rtt != 30*time.Millisecond {
		t.Errorf("expected data RTT of 30ms, got %v", rtt)
	}
}

func TestKarnsAlgorithm(t *testing.T) {
	rt := newRTTTracker()

	t0 := time.Now()
	rt.RecordSend(1000, t0)

	// Mark as retransmitted
	rt.RecordRetransmit(1000)

	// ACK arrives — should be discarded per Karn's algorithm
	rtt := rt.RecordACK(1100, t0.Add(50*time.Millisecond))

	if rtt != 0 {
		t.Errorf("expected RTT=0 for retransmitted segment, got %v", rtt)
	}
}

func TestSRTTSmoothing(t *testing.T) {
	rt := newRTTTracker()

	t0 := time.Now()

	// First sample: 100ms
	rt.RecordSend(1000, t0)
	rt.RecordACK(1100, t0.Add(100*time.Millisecond))

	// SRTT should be exactly 100ms after first sample
	if rt.SRTT() != 100*time.Millisecond {
		t.Errorf("expected SRTT of 100ms after first sample, got %v", rt.SRTT())
	}

	// Second sample: 20ms
	rt.RecordSend(1100, t0.Add(100*time.Millisecond))
	rt.RecordACK(1200, t0.Add(120*time.Millisecond))

	// SRTT = 0.875 * 100 + 0.125 * 20 = 90ms
	expected := time.Duration(float64(100*time.Millisecond)*0.875 + float64(20*time.Millisecond)*0.125)
	got := rt.SRTT()

	diff := got - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Microsecond {
		t.Errorf("expected SRTT ~%v, got %v", expected, got)
	}
}

func TestUnackedEviction(t *testing.T) {
	rt := newRTTTracker()
	t0 := time.Now()

	// Fill up to maxUnackedEntries + 1
	for i := 0; i <= maxUnackedEntries; i++ {
		rt.RecordSend(uint32(i*100), t0.Add(time.Duration(i)*time.Millisecond))
	}

	// Should have evicted the oldest, keeping maxUnackedEntries
	if len(rt.unacked) != maxUnackedEntries {
		t.Errorf("expected %d unacked entries, got %d", maxUnackedEntries, len(rt.unacked))
	}
}
