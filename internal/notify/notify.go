// Package notify handles the "don't bother me unless it matters" layer.
//
// Herdr's standout behavior is not detecting states — it is knowing which
// states are worth interrupting a human for. panopticon adopts the same rule:
// surface a notification only when an agent transitions to blocked (needs
// input) or when a background turn completes unseen (done), and never on the
// ordinary output of an working agent.
package notify

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"panopticon/internal/model"
)

// Notifier is configurable for tests.
type Notifier interface {
	Blocked(a *model.Agent)
	Done(a *model.Agent)
	Stalled(a *model.Agent)
}

// StdNotifier emits OS notifications. On macOS it uses osascript (Terminal
// notifications / Sound); on Linux it falls back to the terminal bell escape.
type StdNotifier struct {
	// Stall the same agent's notifications (debounce) — default 60s.
	minInterval time.Duration

	// Quiet disables sounds.
	Quiet bool

	// NoTuiEnv prevents notifying when running headless (e.g. cron). Always
	// off for now; channel for control.
	last map[string]time.Time
	mu   sync.Mutex
}

func New(minInterval time.Duration) *StdNotifier {
	if minInterval <= 0 {
		minInterval = 60 * time.Second
	}
	return &StdNotifier{
		minInterval: minInterval,
		last:        map[string]time.Time{},
	}
}

func (n *StdNotifier) debounce(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if last, ok := n.last[key]; ok && now.Sub(last) < n.minInterval {
		return true
	}
	n.last[key] = now
	return false
}

func (n *StdNotifier) Blocked(a *model.Agent) {
	if n.debounce("blocked:" + a.Name) {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		osascript(fmt.Sprintf("display notification %q with title %q", a.Name+" needs input (blocked)", "panopticon"))
	case "linux":
		bel()
	}
}

func (n *StdNotifier) Done(a *model.Agent) {
	if n.debounce("done:" + a.Name) {
		return
	}
	// Background done is worth a subtle note too; same channel, lower urgency.
	switch runtime.GOOS {
	case "darwin":
		osascript(fmt.Sprintf("display notification %q with title %q", a.Name+" finished a background turn (done)", "panopticon"))
	case "linux":
		bel()
	}
}

func (n *StdNotifier) Stalled(a *model.Agent) {
	if n.debounce("stalled:" + a.Name) {
		return
	}
	// A stuck agent is worth interrupting for: likely a hung LLM/API call.
	switch runtime.GOOS {
	case "darwin":
		osascript(fmt.Sprintf("display notification %q with title %q", a.Name+" appears stalled (no progress)", "panopticon"))
	case "linux":
		bel()
	}
}

func osascript(script string) {
	// Run detached with a timeout so a missing/broken notification daemon
	// cannot wedge the notifier (or hang the daemon on shutdown).
	go func() {
		cmd := exec.Command("osascript", "-e", script)
		timer := time.AfterFunc(5*time.Second, func() { _ = cmd.Process.Kill() })
		defer timer.Stop()
		_ = cmd.Run()
	}()
}

func bel() {
	// Write the terminal bell to os.Stderr; visible in an attached terminal.
	fmt.Fprint(os.Stderr, "\x07")
}
