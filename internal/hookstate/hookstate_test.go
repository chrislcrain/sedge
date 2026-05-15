package hookstate

import (
	"testing"
	"time"
)

// Classify is small enough to unit-test exhaustively, but the cases that
// actually matter are the three corners of the staleness/disable axis
// introduced by SPEC §4.5's `hook_stale_minutes`:
//
//  1. default 10m demotes after 11m,
//  2. a custom 1m threshold demotes after 2m,
//  3. staleAfter == 0 (disabled) never demotes regardless of age.
//
// Everything else (event → activity mapping, empty event) is covered as
// well so a future change to the switch doesn't quietly regress.

// inFlightEvent is any event that maps to ActivityInFlight when fresh —
// using PreToolUse since it's the canonical "claude is working" signal.
const inFlightEvent = EventPreToolUse

func TestClassify_Default10mDemotesAfter11m(t *testing.T) {
	// Fresh: still in-flight.
	fresh := State{Event: inFlightEvent, At: time.Now().Add(-9 * time.Minute)}
	if got := Classify(fresh, 10*time.Minute); got != ActivityInFlight {
		t.Errorf("9m-old PreToolUse under 10m threshold: got %v, want ActivityInFlight", got)
	}

	// Past threshold: demoted to idle.
	stale := State{Event: inFlightEvent, At: time.Now().Add(-11 * time.Minute)}
	if got := Classify(stale, 10*time.Minute); got != ActivityIdle {
		t.Errorf("11m-old PreToolUse under 10m threshold: got %v, want ActivityIdle (crash fallback)", got)
	}
}

func TestClassify_Custom1mDemotesAfter2m(t *testing.T) {
	// Test-harness-style tight threshold: 1 minute.
	fresh := State{Event: inFlightEvent, At: time.Now().Add(-30 * time.Second)}
	if got := Classify(fresh, 1*time.Minute); got != ActivityInFlight {
		t.Errorf("30s-old PreToolUse under 1m threshold: got %v, want ActivityInFlight", got)
	}

	stale := State{Event: inFlightEvent, At: time.Now().Add(-2 * time.Minute)}
	if got := Classify(stale, 1*time.Minute); got != ActivityIdle {
		t.Errorf("2m-old PreToolUse under 1m threshold: got %v, want ActivityIdle", got)
	}
}

func TestClassify_ZeroDisablesFallback(t *testing.T) {
	// SPEC §4.5: "setting it to 0 disables the fallback entirely (file
	// age never demotes activity)". A 24-hour-old PreToolUse should
	// still classify as ActivityInFlight when the threshold is 0.
	ancient := State{Event: inFlightEvent, At: time.Now().Add(-24 * time.Hour)}
	if got := Classify(ancient, 0); got != ActivityInFlight {
		t.Errorf("24h-old PreToolUse with staleAfter=0: got %v, want ActivityInFlight (fallback disabled)", got)
	}

	// And a Notification just as ancient should still classify as
	// ActivityApprovalPending — the disable applies uniformly across
	// activity buckets, not just in-flight.
	ancientNotify := State{Event: EventNotification, At: time.Now().Add(-24 * time.Hour)}
	if got := Classify(ancientNotify, 0); got != ActivityApprovalPending {
		t.Errorf("24h-old Notification with staleAfter=0: got %v, want ActivityApprovalPending", got)
	}

	// Negative threshold behaves the same as 0 — defensive on the
	// off-chance a caller threads in a negative duration directly
	// (the config loader clamps the toml value, but Classify is a
	// public API and shouldn't trust its inputs blindly).
	if got := Classify(ancient, -1*time.Minute); got != ActivityInFlight {
		t.Errorf("negative staleAfter: got %v, want ActivityInFlight (treated as disabled)", got)
	}
}

func TestClassify_EmptyEventAlwaysIdle(t *testing.T) {
	// A never-written state file looks like the zero value. Should
	// always be idle, regardless of staleAfter. Covers the empty-file
	// path independently of the staleness branch.
	empty := State{}
	for _, threshold := range []time.Duration{0, 10 * time.Minute, time.Hour} {
		if got := Classify(empty, threshold); got != ActivityIdle {
			t.Errorf("empty State with staleAfter=%v: got %v, want ActivityIdle", threshold, got)
		}
	}
}

func TestClassify_EventBucketsFresh(t *testing.T) {
	// Fresh events of each kind map to the documented bucket. Guards
	// against future churn in the switch statement.
	now := time.Now()
	cases := []struct {
		ev   Event
		want Activity
	}{
		{EventUserPromptSubmit, ActivityInFlight},
		{EventPreToolUse, ActivityInFlight},
		{EventPostToolUse, ActivityInFlight},
		{EventSubagentStop, ActivityInFlight},
		{EventNotification, ActivityApprovalPending},
		{EventStop, ActivityIdle},
		{EventSessionEnd, ActivityIdle},
		{EventSessionStart, ActivityIdle},
		{EventPreCompact, ActivityIdle},
	}
	for _, tc := range cases {
		s := State{Event: tc.ev, At: now}
		if got := Classify(s, 10*time.Minute); got != tc.want {
			t.Errorf("fresh %s: got %v, want %v", tc.ev, got, tc.want)
		}
	}
}
