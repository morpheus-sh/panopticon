// Package agent implements panopticon's semantic supervision brain.
//
// This is the piece tmux (and tmux's own plugins) do not give you: turning a
// raw ANSI output stream into a per-agent lifecycle state machine. Herdr
// ships deep per-agent native integrations for 15+ agents; panopticon takes a
// deliberately lean, agent-kind-agnostic approach with strong heuristics, so
// it covers Claude Code, Codex, opencode and Pi without native hooks. That
// trades a little fidelity for a lot of portability — and it is the part we
// can keep improving independently of any engine.
package agent

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"panopticon/internal/model"
)

// blockedRe matches terminal prompts that mean an agent is waiting for user
// input: approval prompts, question UIs, permission dialogs, confirmation
// overlays. These are the (few) moments worth interrupting a human for.
var blockedRe = regexp.MustCompile(`(?mi)` +
	`(allow |approv|would you like|do you want to|proceed\??|permission|` +
	`y[_-]?n[):>?]|REQ_PROCEED|Shift\+Tab to|` +
	`choose (a|an|the) (text style|option|theme|configuration|setup)|` +
	`select a (key|file|directory)|to change this later|` +
	`[•❯>] {0,2}[0-9]{0,2}\.? {0,2}(Dark|Light) mode|` +
	`(accept|authorize)[ \t]|needs your (attention|input)|waiting for (you|your)|` +
	`authentication required|sign in|log in to|authenticate|` +
	`(would you like to approve|are you sure|confirm|press (enter|a key) to))`)

// doneRe matches terminal frames that signal an agent finished a turn.
// Kept conservative and simple to avoid regex-compile brittleness.
var doneRe = regexp.MustCompile(`(?mi)` +
	`(success|passed|failures?: 0|finished|completed|` +
	`all checks passed|worklog saved|=== done ===|\[done\]|\[\$\])`)

// shellPromptRe matches a bash/zsh/fish prompt at the end of output, which is
// the tell-tale sign an agent process has exited back to the shell. When a
// previously-blocked agent returns to a shell prompt, it is done, not blocked.
var shellPromptRe = regexp.MustCompile(`(?m)[$#%>]\s*$`)

// workingRe/activity classification is mostly a function of output volume.
// We use the presence of "thinking/working" markers plus high throughput.

// Identify returns a recognized model.AgentKind given the buffered output and
// the command line that started the pane (when known).
func Identify(knowName string, output string) model.AgentKind {
	o := output
	low := strings.ToLower(o)
	switch {
	case knowName != "":
		// explicit kind hint wins if it parses
		switch model.AgentKind(knowName) {
		case model.KindClaude, model.KindCodex, model.KindOpencode, model.KindPi:
			return model.AgentKind(knowName)
		}
	}
	switch {
	case strings.Contains(low, "claude") || strings.Contains(low, "anthropic"):
		return model.KindClaude
	case strings.Contains(low, "codex") || strings.Contains(low, "openai codex"):
		return model.KindCodex
	case strings.Contains(low, "opencode"):
		return model.KindOpencode
	case strings.Contains(low, "pi ") || strings.Contains(low, "pi:"):
		return model.KindPi
	default:
		return model.KindGeneric
	}
}

// Classifier consumes a stream of output chunks and reports lifecycle-state
// transitions. It is agnostic of the specific agent binary.
type Classifier struct {
	// buffered output window over which detection runs.
	buf string

	// lastTally tracks recent activity to distinguish working vs idle.
	lastActivity time.Time

	// silentFor is set when we want to wait before declaring idle (to let
	// long-output turns settle).
	current model.AgentState

	// onState is invoked with (agent, newState) on every transition.
	onState func(*model.Agent, model.AgentState)

	// boundAt is when the agent was (re)bound to the pane. For startupGrace
	// seconds afterward the classifier suppresses output-state transitions:
	// a freshly-launched agent (or the shell echoing the launch command) must
	// not be mislabeled blocked/working on noise before its real UI paints.
	boundAt time.Time

	// mu guards transient bookkeeping; detection ordering is per-pane so a
	// light lock suffices.
	mu sync.Mutex
}

func NewClassifier(onState func(*model.Agent, model.AgentState)) *Classifier {
	return &Classifier{
		lastActivity: time.Now(),
		current:      model.StateIdle,
		onState:      onState,
	}
}

// Bound marks the agent as freshly bound to the pane, starting the startup
// grace window during which output-state transitions are suppressed.
func (c *Classifier) Bound() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.boundAt = time.Now()
	c.buf = "" // drop pre-binding scrollback/echo noise
	c.current = model.StateWorking
}

// maxBuf caps how much history the classifier holds for detection.
const maxBuf = 20000

// startupGrace is how long after Binding we suppress output-state transitions
// so a freshly-launched agent (and the shell echo that launched it) do not
// get misread as blocked/working before its real UI paints.
const startupGrace = 1800 * time.Millisecond

// Feed chunks a continuation of the pane output into the classifier and
// returns the new inferred state.
func (c *Classifier) Feed(a *model.Agent, chunk []byte) model.AgentState {
	if len(chunk) == 0 {
		return c.current
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	text := strip(chunk) // strip escape sequences for heuristic analysis
	if text == "" {
		return c.current
	}

	// Update running buffer (bounded).
	c.buf += text
	if len(c.buf) > maxBuf {
		if cut := len(c.buf) - maxBuf; cut < len(c.buf) {
			c.buf = c.buf[cut:]
		}
	}

	// Activity just happened.
	c.lastActivity = time.Now()

	// Within the startup grace, do not transition off the initial working
	// guess regardless of what the buffer shows.
	if time.Since(c.boundAt) < startupGrace {
		return model.StateWorking
	}

	st := classify(c.buf, c.lastActivity)
	c.emitLocked(a, st)
	return st
}

// IdleAfter resets the classifier after a period with no output; callers drive
// this from a timer so "settled idle" can become "done" for unseen work.
func (c *Classifier) IdleAfter(a *model.Agent) model.AgentState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.boundAt) < startupGrace {
		return model.StateWorking
	}
	st := classify(c.buf, c.lastActivity)
	c.emitLocked(a, st)
	return st
}

func (c *Classifier) emitLocked(a *model.Agent, st model.AgentState) {
	if st != c.current && c.onState != nil {
		c.current = st
		if os.Getenv("PANOPTICON_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[panopticon] agent %s -> %s (bufTail=%q)\n", a.Name, st, tail(c.buf, 80))
		}
		c.onState(a, st)
	}
}

// classify is pure: given the buffered text and last-activity time, decide the
// current lifecycle state.
//
// Priority order matters: semantic signals (blocked/done markers) take
// precedence over the activity heuristic. That way a freshly-printed approval
// prompt is recognized as blocked immediately, not lazily as "working" because
// output just arrived.
func classify(buf string, last time.Time) model.AgentState {
	// Keep the tail for recency-scoped heuristics.
	tailTxt := tail(buf, 4000)

	// 0. A shell prompt at the end of output means the agent process has
	//    exited back to the shell. This overrides a stale 'blocked' (the
	//    process is gone, so it cannot truly be waiting on input).
	if shellPromptRe.MatchString(tailTxt) {
		return model.StateDone
	}

	// 1. Blocked is the highest live-priority, interruptible signal: an agent
	//    is showing an approval/question/selector prompt and is waiting on
	//    input (its own UI is painted on the screen).
	if blockedRe.MatchString(tailTxt) {
		return model.StateBlocked
	}

	// 2. Done: a clear turn-completion frame. A freshly-printed completion
	//    line is itself the signal that the turn ended, so we favor it over
	//    the activity heuristic. (The recheck timer converges anyway.)
	if doneRe.MatchString(tailTxt) {
		return model.StateDone
	}

	// 3. Otherwise trust the activity heuristic.
	if time.Since(last) < 1200*time.Millisecond {
		return model.StateWorking
	}

	// 4. Idle.
	return model.StateIdle
}

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if n >= len(s) {
		return s
	}
	return s[len(s)-n:]
}

// strip removes ANSI sequences and control chars for heuristic matching.
func strip(b []byte) string {
	s := string(b)
	var ansiUnsafe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*\x07`)
	s = ansiUnsafe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
