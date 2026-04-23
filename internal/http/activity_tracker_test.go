package http

import (
	"testing"
	"time"
)

func TestActivityTracker_FirstSightNeedsSignal(t *testing.T) {
	tr := newActivityTracker()
	now := time.Unix(1_700_000_000, 0)
	tr.nowFunc = func() time.Time { return now }

	// Fresh user with zero bytes and offline: hidden. This is what
	// happens right after Reset stats — we don't want the list to
	// repopulate from scratch.
	if tr.observe("alice", 0, false) {
		t.Errorf("zero-byte offline first sight should stay hidden")
	}
	// First real traffic: visible.
	if !tr.observe("alice", 100, false) {
		t.Errorf("byte delta should make alice visible")
	}
	// Still within idle window.
	now = now.Add(5 * time.Minute)
	if !tr.observe("alice", 100, false) {
		t.Errorf("still within idle window")
	}
	// Past idle window, no change, offline: hidden.
	now = now.Add(activityIdleHide + time.Second)
	if tr.observe("alice", 100, false) {
		t.Errorf("expected hidden after idle cutoff")
	}
	// Online revives even without byte delta.
	if !tr.observe("alice", 100, true) {
		t.Errorf("online should always be visible")
	}
}

func TestActivityTracker_FirstSightOnlineVisible(t *testing.T) {
	tr := newActivityTracker()
	tr.nowFunc = func() time.Time { return time.Unix(1_700_000_000, 0) }
	// Zero bytes but online: visible.
	if !tr.observe("bob", 0, true) {
		t.Errorf("online first sight should be visible")
	}
}

func TestActivityTracker_FirstSightWithTrafficVisible(t *testing.T) {
	// Panel just started but xray has been running — alice already has
	// cumulative bytes. She should appear immediately.
	tr := newActivityTracker()
	tr.nowFunc = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if !tr.observe("alice", 500, false) {
		t.Errorf("first sight with non-zero traffic should be visible")
	}
}

func TestActivityTracker_Reset(t *testing.T) {
	tr := newActivityTracker()
	base := time.Unix(1_700_000_000, 0)
	tr.nowFunc = func() time.Time { return base }
	tr.observe("alice", 100, false) // visible
	tr.reset()
	// After reset counters are zero (xray was just reset); alice
	// should not reappear until she does something.
	if tr.observe("alice", 0, false) {
		t.Errorf("after reset with zero bytes, alice should be hidden")
	}
}
