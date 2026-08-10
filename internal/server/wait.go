package server

import (
	"fmt"
	"time"

	"panopticon/internal/model"
)

// awaitState blocks until the agent reaches one of the given states or timeout.
// It reads the agent's state through model.Server (which owns the lock the
// detector writes under) so there is no data race and no deadlock on the
// server itself while waiting.
func (s *Server) awaitState(a *model.Agent, states []model.AgentState, timeout time.Duration) (model.AgentState, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		cur := s.model.AgentState(a)
		for _, want := range states {
			if cur == want {
				return cur, nil
			}
		}
		if time.Now().After(deadline) {
			return cur, fmt.Errorf("timed out after %v waiting for %v; last state %s", timeout, states, cur)
		}
		<-tick.C
	}
}

// awaitAnyChange reports whether the agent transitioned away from its state at
// call time within timeout. Used for the 5s agent_prompt_stalled watchdog.
func (s *Server) awaitAnyChange(a *model.Agent, timeout time.Duration) (bool, error) {
	start := s.model.AgentState(a)
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		if cur := s.model.AgentState(a); cur != start {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		<-tick.C
	}
}
