package agent_test

import (
	"testing"
	"time"

	"panopticon/internal/agent"
	"panopticon/internal/model"
)

func newClassifier() *agent.Classifier {
	// Not Bound(), so boundAt is zero and the startup grace is already past:
	// enables immediate semantic classification in tests.
	return agent.NewClassifier(func(a *model.Agent, st model.AgentState) {})
}

// TestDetectBlockedPrompt verifies that a classic agent approval prompt is
// classified as blocked.
func TestDetectBlockedPrompt(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want model.AgentState
	}{
		{"approval-do-you-want", "Do you want to proceed? (y/n)", model.StateBlocked},
		{"approve-files", "Would you like to approve and run terminal commands?", model.StateBlocked},
		{"permission", "Permission to run this command? [y/N]", model.StateBlocked},
		{"theme-wizard", "Choose the text style that looks best with your terminal", model.StateBlocked},
		{"question-marker", "❯ 1. Dark mode\n   2. Light mode", model.StateBlocked},
		{"std-approval", "[blocked] Do you want to proceed? (y/n)", model.StateBlocked},
		{"plain-output", "compiling src/main.rs...", model.StateWorking},
		{"finished", "✓ Finished. 14 passed · 0 failed · 41.3s", model.StateDone},
		// A shell prompt after output means the agent exited: done, not blocked.
		{"shell-prompt-overrides", "[blocked] Do you want to proceed? (y/n)\nnickdhima@host dir %", model.StateDone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clf := newClassifier()
			a := &model.Agent{Name: "x", Kind: model.KindGeneric}
			got := clf.Feed(a, []byte(tc.out))
			if got != tc.want {
				t.Errorf("Feed(%q) = %s, want %s", tc.out, got, tc.want)
			}
		})
	}
}

// TestDetectLifecycle verifies the state machine drives working -> done ->
// blocked in order and transitions only on semantic markers.
func TestDetectLifecycle(t *testing.T) {
	clf := newClassifier()
	a := &model.Agent{Name: "x", Kind: model.KindGeneric}

	// Fresh chunk output => working.
	if got := clf.Feed(a, []byte("applying patch")); got != model.StateWorking {
		t.Fatalf("working: got %s", got)
	}
	// Completion marker => done.
	if got := clf.Feed(a, []byte("\n[done] 14 passed · 0 failed")); got != model.StateDone {
		t.Fatalf("done: got %s", got)
	}
	// Approval prompt => blocked (takes precedence).
	if got := clf.Feed(a, []byte("\n[blocked] Do you want to proceed? (y/n)")); got != model.StateBlocked {
		t.Fatalf("blocked: got %s", got)
	}
}

// TestDetectStall verifies that once a spinner-only agent has gone past the
// stall threshold with no real progress, it is declared 'stalled' rather than
// 'working', and that new meaningful text resumes it immediately.
//
// The stall threshold is a real-time budget, so this test sleeps across it.
func TestDetectStall(t *testing.T) {
	a := &model.Agent{Name: "x", Kind: model.KindGeneric}
	clf := agent.NewClassifier(func(_ *model.Agent, st model.AgentState) {})

	// Real work first -> working, and set lastMeaningful.
	if got := clf.Feed(a, []byte("analyzing")); got != model.StateWorking {
		t.Fatalf("working: got %s", got)
	}

	// Wait out the startup grace so our feeds actually classify.
	time.Sleep(2 * time.Second)

	// Spin (identical frames redrawn in place via \r) with plenty of wall-clock
	// time beyond the stall threshold so a stall is observable.
	deadline := time.Now().Add(10 * time.Second)
	stalled := false
	for time.Now().Before(deadline) {
		if got := clf.Feed(a, []byte("\r⠋ Working...")); got == model.StateStalled {
			stalled = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !stalled {
		t.Fatal("never observed stalled state for spinner-only agent")
	}

	// New meaningful text resumes working immediately.
	if got := clf.Feed(a, []byte("patch applied")); got != model.StateWorking {
		t.Fatalf("resume working, got %s", got)
	}
}
