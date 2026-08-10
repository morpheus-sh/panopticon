// Package server wires the engine, model, agent detection, notifications and
// the JSON socket API into the panopticon daemon.
//
// Lifecycle:
//   - The daemon owns a set of tmux sessions (one per Workspace). It injects
//     HERDR_* -style caller-context env into every pane it creates so agents in
//     panes can find the API (mirrors Herdr's HERDR_WORKSPACE_ID / TAB_ID /
//     PANE_ID / PANOPTICON_SOCKET env).
//   - Each workspace pulls its tabs/panes from the engine and starts a
//     per-pane agent Classifier reading the engine Stream.
//   - The unix socket accepts JSON-lines requests and dispatches to the
//     command handlers.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"net"
	"panopticon/internal/agent"
	"panopticon/internal/api"
	"panopticon/internal/engine"
	"panopticon/internal/ipc"
	"panopticon/internal/model"
	"panopticon/internal/notify"
)

// Version of the panopticon daemon.
const Version = "0.1.0"

// SocketPathEnv names the env var injected into panes pointing at the API
// socket. Mirrors Herdr's HERDR_* caller-context pattern.
const SocketPathEnv = "PANOPTICON_SOCKET"

// Server is the panopticon daemon.
type Server struct {
	eng      engine.Engine
	model    *model.Server
	notifier notify.Notifier
	clfrs    map[string]*agent.Classifier // paneID -> classifier
	stops    map[string]chan struct{}     // paneID -> stop signal

	// socket
	listener net.Listener
	sockPath string

	// mu serializes structure mutation; handlers run serially.
	mu sync.Mutex
}

// Config describes server construction.
type Config struct {
	ServerName string // tmux -L server token
	SocketPath string // API socket path (auto-derived if empty)
	Notifier   notify.Notifier
}

// New builds a Server with a fresh (or adopted) tmux server.
func New(cfg Config) (*Server, error) {
	if cfg.ServerName == "" {
		cfg.ServerName = "panopticon"
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = ipc.DefaultSocket(cfg.ServerName)
	}
	if cfg.Notifier == nil {
		cfg.Notifier = notify.New(60 * time.Second)
	}
	s := &Server{
		eng:      engine.NewTmux(cfg.ServerName),
		model:    model.NewServer(),
		notifier: cfg.Notifier,
		clfrs:    map[string]*agent.Classifier{},
		stops:    map[string]chan struct{}{},
		sockPath: cfg.SocketPath,
	}
	return s, nil
}

// ---- engine reconciliation ----

// Start performs initial engine reconciliation: adopt the session, create the
// root workspace if none exists, and begin stream classification.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Root workspace mirrors a tmux session named if present.
	ws, tab := s.model.NewWorkspace("main")

	// Create the tmux session engine-side.
	tmuxPane, err := s.eng.CreateSession(ws.Name, 200, 50)
	if err != nil {
		return fmt.Errorf("engine create session: %w", err)
	}
	// Register the root pane, binding model pane to engine pane.
	p := s.model.AddPane(tab, tmuxPane, "main")

	// Give the shell a moment, then attach env + start detecting.
	return s.attachPaneDetection(p.ID, tmuxPane)
}

// attachPaneDetection wires env context and starts a classifier on a pane.
func (s *Server) attachPaneDetection(paneID, tmuxPaneID string) error {
	clf := agent.NewClassifier(s.onAgentState)
	s.clfrs[paneID] = clf
	stop := make(chan struct{})
	s.stops[paneID] = stop

	// Start the engine stream in the background.
	go s.runDetection(paneID, tmuxPaneID, clf, stop)
	return nil
}

func (s *Server) runDetection(paneID, tmuxPaneID string, clf *agent.Classifier, stop chan struct{}) {
	// Start an idle-timer that periodically re-evaluates settled states.
	recheck := time.NewTicker(2 * time.Second)
	defer recheck.Stop()

	// Feedback from engine stream.
	errCh := make(chan error, 1)

	// In a background goroutine, call Stream (blocking) with a callback that
	// feeds the classifier and notifies.
	go func() {
		errCh <- s.eng.Stream(tmuxPaneID, func(chunk []byte) {
			// Find the model pane and agent.
			mp := s.model.ResolvePane(paneID)
			if mp == nil {
				return
			}
			if mp.Agent == nil {
				return
			}
			clf.Feed(mp.Agent, chunk)
		}, stop)
	}()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				// Pane likely closed.
				s.releasePane(paneID)
			}
			return
		case <-recheck.C:
			mp := s.model.ResolvePane(paneID)
			if mp == nil || mp.Agent == nil {
				continue
			}
			clf.IdleAfter(mp.Agent)
		case <-stop:
			return
		}
	}
}

// onAgentState is called by a classifier on lifecycle transitions.
func (s *Server) onAgentState(a *model.Agent, st model.AgentState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.model.SetAgentState(a, st)
	if !changed {
		return
	}
	// Notifications only for interruptible states.
	switch st {
	case model.StateBlocked, model.StateDone:
		if st == model.StateBlocked {
			s.notifier.Blocked(a)
		} else {
			s.notifier.Done(a)
		}
	}
}

func (s *Server) releasePane(paneID string) {
	// Release any agent bound to the pane so its name/pointer no longer leak.
	// Runs after the stop signal is delivered; safe to release here.
	p, _ := s.model.SnapshotPane(paneID)
	if p.PaneID != "" && p.Agent != nil {
		s.model.ReleaseAgentName(p.Agent.Name)
	}
	s.mu.Lock()
	delete(s.stops, paneID)
	delete(s.clfrs, paneID)
	s.mu.Unlock()
}

// ---- socket listener ----

// Listen binds the API socket and starts accepting requests.
func (s *Server) Listen() error {
	sockPath := s.sockPath
	if sockPath == "" {
		sockPath = ipc.DefaultSocket("panopticon")
		s.sockPath = sockPath
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	s.listener = ln
	go s.acceptLoop()
	return nil
}

// Shutdown stops the daemon cleanly: closes the listener and detection
// goroutines, and removes the socket file. User panes are not killed — the
// tmux sessions persist so a later restart re-adopts them via CreateSession.
func (s *Server) Shutdown() {
	s.mu.Lock()
	for _, stop := range s.stops {
		close(stop)
	}
	s.stops = map[string]chan struct{}{}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	sock := s.sockPath
	s.mu.Unlock()
	if sock != "" {
		_ = os.Remove(sock)
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(c net.Conn) {
	defer c.Close()
	dec := json.NewDecoder(c)
	enc := json.NewEncoder(c)
	for {
		var req api.Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			// Send protocol error and drop.
			_ = enc.Encode(api.Failure(req, -32700, "parse error: "+err.Error()))
			return
		}
		resp := s.dispatch(req)
		if resp.ID == "" {
			resp.ID = req.ID
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// ---- dispatch ----
//
// Handlers are dispatched WITHOUT holding s.mu. Blocking handlers such as
// agent.wait / agent.prompt --wait poll the agent state for up to minutes;
// holding the mutex across the wait would both deadlock (Go's Mutex is not
// reentrant — the polling helper re-locks) and stall every other request.
// Handlers lock s.mu themselves for the short critical sections that mutate
// server-level shared state (pane/agent maps). Model operations are already
// individually synchronized by model.Server.
func (s *Server) dispatch(req api.Request) api.Response {
	switch req.Method {
	case "ping":
		return api.Success(map[string]string{"pong": Version})
	case "workspace.list":
		return api.Success(s.WorkspaceList())
	case "workspace.create":
		return s.WorkspaceCreate(req)
	case "workspace.current":
		return s.WorkspaceCurrent(req)
	case "tab.list":
		return s.TabList(req)
	case "pane.list":
		return s.PaneList(req)
	case "pane.current":
		return s.PaneCurrent(req)
	case "pane.split":
		return s.PaneSplit(req)
	case "pane.run":
		return s.PaneRun(req)
	case "pane.read":
		return s.PaneRead(req)
	case "pane.wait-output":
		return s.PaneWaitOutput(req)
	case "agent.list":
		return api.Success(s.AgentList())
	case "agent.get":
		return s.AgentGet(req)
	case "agent.start":
		return s.AgentStart(req)
	case "agent.prompt":
		return s.AgentPrompt(req)
	case "agent.send-keys":
		return s.AgentSendKeys(req)
	case "agent.wait":
		return s.AgentWait(req)
	case "agent.read":
		return s.AgentRead(req)
	default:
		return api.Failure(req, -32601, "method not found: "+req.Method)
	}
}
