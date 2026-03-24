package flow

import (
	"testing"
	"time"
)

func TestSpeedTrackerBurst(t *testing.T) {
	st := newSpeedTracker()
	t0 := time.Now()

	// Simulate a download burst: 1MB over 100ms = 10 MB/s
	for i := 0; i < 100; i++ {
		st.RecordData(t0.Add(time.Duration(i)*time.Millisecond), 0, 10000)
	}

	dl, ul := st.DrainSpeeds()
	if len(ul) != 0 {
		t.Errorf("expected no upload speeds, got %d", len(ul))
	}
	if len(dl) != 1 {
		t.Fatalf("expected 1 download burst, got %d", len(dl))
	}

	// ~10 MB/s (1M bytes / 0.099s)
	if dl[0] < 5e6 || dl[0] > 15e6 {
		t.Errorf("expected ~10 MB/s, got %.0f", dl[0])
	}
}

func TestSpeedTrackerMultipleBursts(t *testing.T) {
	st := newSpeedTracker()
	t0 := time.Now()

	// First burst: 50ms
	for i := 0; i < 50; i++ {
		st.RecordData(t0.Add(time.Duration(i)*time.Millisecond), 0, 5000)
	}

	// 500ms gap — triggers new burst
	t1 := t0.Add(550 * time.Millisecond)
	for i := 0; i < 50; i++ {
		st.RecordData(t1.Add(time.Duration(i)*time.Millisecond), 0, 10000)
	}

	dl, _ := st.DrainSpeeds()
	if len(dl) != 2 {
		t.Errorf("expected 2 bursts, got %d", len(dl))
	}
}

func TestSpeedTrackerShortBurstIgnored(t *testing.T) {
	st := newSpeedTracker()
	t0 := time.Now()

	// Single packet "burst" — less than 10ms, should be ignored
	st.RecordData(t0, 0, 1000)
	st.RecordData(t0.Add(5*time.Millisecond), 0, 1000)

	dl, _ := st.DrainSpeeds()
	if len(dl) != 0 {
		t.Errorf("expected no bursts for <10ms duration, got %d", len(dl))
	}
}

func TestSpeedTrackerDrainResets(t *testing.T) {
	st := newSpeedTracker()
	t0 := time.Now()

	for i := 0; i < 50; i++ {
		st.RecordData(t0.Add(time.Duration(i)*time.Millisecond), 1000, 0)
	}

	_, ul1 := st.DrainSpeeds()
	if len(ul1) == 0 {
		t.Fatal("expected upload speeds from first drain")
	}

	// Second drain should be empty
	_, ul2 := st.DrainSpeeds()
	if len(ul2) != 0 {
		t.Errorf("expected no speeds after second drain, got %d", len(ul2))
	}
}
