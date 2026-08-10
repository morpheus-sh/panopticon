package agent_test

import (
	"testing"

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
