// Package engine is the portability seam of panopticon.
//
// It encapsulates every interaction with the underlying terminal multiplexing
// backend (currently tmux). No other package calls tmux directly. If the
// backend ever changes, only this package is rewritten — the model, agent
// detection, API and skill contract stay identical. That is the concrete
// anti-lock-in guarantee that makes panopticon competitive and portable.
package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Engine is the multiplexer backend contract panopticon depends on.
type Engine interface {
	// CreateSession starts a detached session named name and returns an
	// engine pane id of its initial pane. wide/tall set the initial pane size
	// in cells (0 = default).
	CreateSession(name string, wide, tall int) (string, error)

	// SplitPane splits the engine pane and returns the new pane id.
	SplitPane(targetPaneID string, horizontal bool, cwd string) (string, error)

	// ClosePane closes a pane.
	ClosePane(paneID string) error

	// SendKeys writes keys (with an implied Enter for SendLine) to a pane.
	SendKeys(paneID, text string, enter bool) error

	// SendKey writes a single tmux key token (e.g. "Enter", "Escape",
	// "C-c", "BTab") to a pane. Unlike SendKeys this is a real keypress,
	// not literal typed text.
	SendKey(paneID, token string) error

	// ReadScrollback returns the terminal text of a pane.
	ReadScrollback(paneID string, lines int) (string, error)

	// ReadVisible returns the currently visible viewport (bottom buffer)
	// of a pane. This is the snapshot used for agent DETECTION: unlike full
	// scrollback it excludes echoed shell command lines and stale history,
	// so a block/approval prompt is not confused with its own echoed text.
	ReadVisible(paneID string, rows int) (string, error)

	// WorkingDir returns the current working directory of the pane's shell.
	WorkingDir(paneID string) (string, error)

	// Cwd sets a pane's working directory for the next spawn.
	Cwd(paneID, dir string) error

	// Focus marks a pane as seen by the human/agent (used for idle/done split).
	Focus(paneID string) error

	// WaitMatch polls until the pane output contains match, returning the
	// matching snapshot, or error after timeout.
	WaitMatch(paneID, match string, regex bool, timeout time.Duration) (string, error)

	// Stream starts sending pane output chunks to the callback until stop is
	// closed. This is the stream used for agent state detection.
	Stream(paneID string, cb func(chunk []byte), stop chan struct{}) error

	// TmuxSessionName returns the bracketing server token for health checks.
	ServerName() string

	// Alive reports whether the engine is healthy.
	Alive() bool

	// Close tears down engine resources (does not kill user panes).
	Close()
}

// ---- errors ----

type NotFoundError struct{ paneID string }

func (e NotFoundError) Error() string { return fmt.Sprintf("pane not found: %s", e.paneID) }

// ---- implementation ----

// Tmux is the tmux-backed engine. All commands are issued through a single
// named server (tmux -L <serverName>) so panopticon owns an isolated set of
// sessions and never touches the user's other tmux sessions.
type Tmux struct {
	serverName string
	bin        string
	mu         sync.Mutex // tmux itself is not fully concurrency-safe per server; serialize
}

// NewTmux creates a tmux engine bound to a named server.
func NewTmux(serverName string) *Tmux {
	bin := "tmux"
	if b := os.Getenv("PANOPTICON_TMUX"); b != "" {
		bin = b
	}
	t := &Tmux{serverName: serverName, bin: bin}
	return t
}

func (t *Tmux) ServerName() string { return t.serverName }

func (t *Tmux) argsList(sub []string) []string {
	args := []string{"-L", t.serverName}
	args = append(args, sub...)
	return args
}

// run executes a tmux command and returns trimmed stdout.
func (t *Tmux) run(sub []string) (string, error) {
	cmd := exec.Command(t.bin, t.argsList(sub)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %v: %v: %s", sub, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (t *Tmux) CreateSession(name string, wide, tall int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cmd := []string{"new-session", "-d", "-s", name}
	if wide > 0 && tall > 0 {
		cmd = append(cmd, "-x", fmt.Sprintf("%d", wide), "-y", fmt.Sprintf("%d", tall))
	}
	if _, err := t.run(cmd); err != nil {
		// A detached panopticon server that crashed and restarted will find
		// the old session (and tmux server) still alive. That is not a fatal
		// error: adopt the existing session rather than die. (duplicate session)
		if strings.Contains(err.Error(), "duplicate session") {
			// fall through and resolve the root pane of the existing session
		} else {
			return "", err
		}
	}
	// Resolve the root pane id of the (newly created or adopted) session.
	pane, err2 := t.run([]string{"display-message", "-p", "-t", name + ":0", "#{pane_id}"})
	if err2 != nil || pane == "" {
		if isWholeSessionMissing(err2) {
			// session did not actually exist; propagate the create error
			return "", err2
		}
		pane = name + "0" // fallback engine id
	}
	return pane, nil
}

// isWholeSessionMissing reports whether the tmux error indicates no matching
// session (so retrying new-session makes sense).
func isWholeSessionMissing(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "can't find session"))
}

func (t *Tmux) SplitPane(targetPaneID string, horizontal bool, cwd string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dir := "down"
	if horizontal {
		dir = "right"
	}
	args := []string{"split-window", "-d"}
	if dir != "down" {
		args = append(args, "-h")
	}
	args = append(args, "-t", targetPaneID)
	args = append(args, "-c", cwdOr(cwd, "."))
	args = append(args, "-P", "-F", "#{pane_id}") // print the new pane id
	out, err := t.run(args)
	if err != nil {
		return "", err
	}
	// With -F we asked for the machine-readable pane id (e.g. %1); if tmux
	// ignored it, fall back to the raw printed token.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			// Prefer a %id token; otherwise accept whatever tmux printed.
			if strings.HasPrefix(line, "%") {
				return line, nil
			}
			return line, nil
		}
	}
	return "", fmt.Errorf("split-window did not report new pane id (got %q)", out)
}

func (t *Tmux) ClosePane(paneID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !strings.HasPrefix(paneID, "%") {
		// our engine ids may be "windex:pindex"; kill by exact match
	}
	_, err := t.run([]string{"kill-pane", "-t", paneID})
	return err
}

func (t *Tmux) SendKeys(paneID, text string, enter bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	args := []string{"send-keys", "-t", paneID, "-l", text}
	if _, err := t.run(args); err != nil {
		return err
	}
	if enter {
		if _, err := t.run([]string{"send-keys", "-t", paneID, "Enter"}); err != nil {
			return err
		}
	}
	return nil
}

// SendKey writes a real tmux keypress by name. It must be a tmux key token
// (Enter, Escape, C-c, BTab, ...). It is deliberately separate from SendKeys
// because tmux treats bare words as literal text; only the special forms
// (capitalized names, C-x, M-x) are interpreted as keys.
func (t *Tmux) SendKey(paneID, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.run([]string{"send-keys", "-t", paneID, name})
	return err
}

func (t *Tmux) ReadScrollback(paneID string, lines int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if lines <= 0 {
		return "", nil
	}
	cap := fmt.Sprintf("#{scroll_position} #{history_size}:#{history_limit} #{pane_height}")
	// Capture the last N lines from history via capture-pane -e -p -S-
	out, err := t.run([]string{"capture-pane", "-t", paneID, "-e", "-p", "-S", "-" + fmt.Sprintf("%d", lines)})
	if err != nil {
		// fall back to plain capture
		out, err = t.run([]string{"capture-pane", "-t", paneID, "-p", "-S", "-" + fmt.Sprintf("%d", lines)})
		if err != nil {
			return "", err
		}
	}
	_ = cap
	return out, nil
}

func (t *Tmux) ReadVisible(paneID string, rows int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rows <= 0 {
		rows = 40
	}
	// capture-pane without -S reads only the visible screen.
	out, err := t.run([]string{"capture-pane", "-t", paneID, "-e", "-p"})
	if err != nil {
		out, err = t.run([]string{"capture-pane", "-t", paneID, "-p"})
		if err != nil {
			return "", err
		}
	}
	return out, nil
}

func (t *Tmux) WorkingDir(paneID string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out, err := t.run([]string{"display-message", "-p", "-t", paneID, "#{pane_current_path}"})
	if err != nil {
		return "", err
	}
	return out, nil
}

func (t *Tmux) Cwd(paneID, dir string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.run([]string{"send-keys", "-t", paneID, "-l", "cd " + quoteShell(dir)})
	if err != nil {
		return err
	}
	_, err = t.run([]string{"send-keys", "-t", paneID, "Enter"})
	return err
}

func (t *Tmux) Focus(paneID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.run([]string{"select-pane", "-t", paneID})
	return err
}

func (t *Tmux) WaitMatch(paneID, match string, regex bool, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	// Attempt in increasing chunks to reduce tmux churn.
	interval := 200 * time.Millisecond
	for {
		snap, err := t.ReadScrollback(paneID, 200)
		if err != nil {
			return "", err
		}
		snapLines := []string{}
		for _, l := range strings.Split(snap, "\n") {
			snapLines = append(snapLines, stripANSI(l))
		}
		if regex {
			if matchesRegex(strings.Join(snapLines, "\n"), match) {
				return snap, nil
			}
		} else if strings.Contains(strings.Join(snapLines, "\n"), match) {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting %v for %q", timeout, match)
		}
		time.Sleep(interval)
		if interval < 2000*time.Millisecond {
			interval *= 2
		}
	}
}

// Stream polls the pane's VISIBLE viewport (bottom buffer) and replays new
// terminal chunks to cb. Detection runs on the living screen, not full
// scrollback: that is what separates a real block prompt from the shell echo
// of a command containing the same words.
func (t *Tmux) Stream(paneID string, cb func(chunk []byte), stop chan struct{}) error {
	var last string
	interval := 350 * time.Millisecond
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		snap, err := t.ReadVisible(paneID, 40)
		if err != nil {
			// pane gone
			return NotFoundError{paneID}
		}
		if snap != last {
			if last != "" {
				// Emit only the delta (best-effort tail diff).
				cb([]byte(diff(last, snap)))
			} else {
				cb([]byte(snap))
			}
			last = snap
		}
		time.Sleep(interval)
	}
}

func (t *Tmux) Alive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.run([]string{"list-sessions"})
	return err == nil
}

func (t *Tmux) Close() {}
