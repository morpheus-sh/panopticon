// Package model defines panopticon's core domain types and ID scheme.
//
// The identifier scheme is deliberately compatible with Herdr's public agent
// contract so that any agent-skill file written for one engine works with the
// other:
//
//	workspace: w1
//	tab:       w1:t1
//	pane:      w1:p1
//
// IDs are opaque, stable handles allocated by the server. Closed pane/tab IDs
// are never reused.
package model

import "fmt"
import "sync"

// Workspace is a top-level grouping of tabs, mirroring a tmux session.
type Workspace struct {
	ID   string
	Name string // tmux session name
	Tabs []*Tab
}

// Tab groups panes, mirroring a tmux window.
type Tab struct {
	// Qualified tab ID, e.g. "w1:t1".
	ID        string
	Workspace *Workspace
	Panes     []*Pane
}

// Pane is a single terminal pane, mirroring a tmux pane.
type Pane struct {
	// Qualified pane ID, e.g. "w1:p1".
	ID   string
	Name string // arbitrary label; defaults to the tmux pane id
	// TmuxPaneID is the engine's native pane identifier. This is the
	// portability seam: only the engine layer ever reads or writes it.
	TmuxPaneID string
	Tab        *Tab
	// Agent is the recognized coding agent currently occupying this pane,
	// or nil if the pane holds ordinary shell/process context.
	Agent *Agent
}

// AgentKind is one of the recognized agent families.
type AgentKind string

const (
	KindUnknown  AgentKind = "unknown"
	KindClaude   AgentKind = "claude"
	KindCodex    AgentKind = "codex"
	KindOpencode AgentKind = "opencode"
	KindPi       AgentKind = "pi"
	KindGeneric  AgentKind = "agent"
)

// AgentState is the recognized lifecycle state of an agent.
type AgentState string

const (
	StateIdle    AgentState = "idle"
	StateWorking AgentState = "working"
	StateBlocked AgentState = "blocked"
	StateDone    AgentState = "done"
	// StateStalled is a working agent that has stopped making real progress
	// (e.g. a spinner that spins with no token growth for a while). It is
	// worth surfacing because it usually means a hung LLM/API call.
	StateStalled AgentState = "stalled"
	StateUnknown AgentState = "unknown"
)

// Agent is a coding agent recognized inside a pane.
type Agent struct {
	// Name is the unique live agent name matching [a-z][a-z0-9_-]{0,31}.
	Name  string
	Kind  AgentKind
	State AgentState
	// Seen marks whether the agent's tab has been focused in the attached UI.
	// Used to distinguish "idle" from "done" (idle after background work).
	Seen bool
}

// AgentInfo is an immutable snapshot of an agent's observable fields, safe to
// read without further locking. It is what the view/API layer consumes so it
// never touches live, concurrently-mutated structs directly.
type AgentInfo struct {
	Name   string
	Kind   AgentKind
	State  AgentState
	Seen   bool
	PaneID string // qualified pane id that hosts this agent ("" if not hosted)
}

// PaneInfo is an immutable snapshot of a pane's observable fields.
type PaneInfo struct {
	PaneID string
	Name   string
	TabID  string
	Agent  *AgentInfo // nil if the pane holds no recognized agent
}

// WorkspaceInfo is an immutable snapshot of a workspace.
type WorkspaceInfo struct {
	WorkspaceID string
	Name        string
	Tabs        int
}

// TabInfo is an immutable snapshot of a tab.
type TabInfo struct {
	TabID string
	Panes int
}

// Server is the in-memory session authority. All access is serialized by the
// model mutex; panopticon's server package may touch this from many goroutines
// (RPC handlers, detection loops) so snapshot accessors are provided.
type Server struct {
	mu sync.Mutex

	// Sequence counter for workspace ID allocation.
	nextWorkspace int

	Workspaces []*Workspace

	// agentsByName indexes live agents for O(1) resolution.
	agentsByName map[string]*Agent
	// panesByID maps qualified and engine pane IDs to their model.
	panesByID map[string]*Pane
}

func NewServer() *Server {
	return &Server{
		agentsByName: make(map[string]*Agent),
		panesByID:    make(map[string]*Pane),
	}
}

// ---- ID allocation ----

func (s *Server) allocWorkspaceID() string {
	s.nextWorkspace++
	return fmt.Sprintf("w%d", s.nextWorkspace)
}

// ---- workspace/tab/pane construction ----

// NewWorkspace creates a workspace and its first tab (no pane — the caller
// creates the engine pane and binds it via AddPane).
func (s *Server) NewWorkspace(name string) (*Workspace, *Tab) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		name = "main"
	}
	w := &Workspace{
		ID:   s.allocWorkspaceID(),
		Name: name,
	}
	s.Workspaces = append(s.Workspaces, w)

	t := &Tab{
		ID:        fmt.Sprintf("%s:t%d", w.ID, 1),
		Workspace: w,
	}
	w.Tabs = append(w.Tabs, t)
	return w, t
}

// AddPane appends a new model pane to a tab (the engine pane must have been
// created already by the engine layer).
func (s *Server) AddPane(t *Tab, tmuxPaneID, name string) *Pane {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &Pane{
		ID:         fmt.Sprintf("%s:p%d", t.Workspace.ID, len(t.Panes)+1),
		Name:       name,
		TmuxPaneID: tmuxPaneID,
		Tab:        t,
	}
	t.Panes = append(t.Panes, p)
	s.panesByID[p.ID] = p
	s.panesByID[tmuxPaneID] = p
	return p
}

// ---- resolution ----

// ResolvePane finds a pane by qualified ID or engine pane ID.
func (s *Server) ResolvePane(id string) *Pane {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panesByID[id]
}

// ResolveAgent finds a live agent by name.
func (s *Server) ResolveAgent(name string) *Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentsByName[name]
}

// BindAgent attaches an agent to a pane, registering its name.
func (s *Server) BindAgent(p *Pane, a *Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Agent = a
	s.agentsByName[a.Name] = a
}

// AgentState returns a snapshot of an agent's current lifecycle state,
// synchronized with writers (SetAgentState).
func (s *Server) AgentState(a *Agent) AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return a.State
}

func (s *Server) SetAgentState(a *Agent, st AgentState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.State == st {
		return false
	}
	a.State = st
	return true
}

// MarkSeen marks an agent's tab as seen in the focused UI.
func (s *Server) MarkSeen(a *Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Seen = true
}

// ReleaseAgentName removes a live agent by name, freeing it for reuse. Used
// when a pane closes and its bound agent goes away.
func (s *Server) ReleaseAgentName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agentsByName, name)
}

// AgentNamesValid checks the public name grammar.
func AgentNameValid(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}
	for i, r := range name {
		if i == 0 && !(r >= 'a' && r <= 'z') {
			return false
		}
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// ---- concurrency-safe snapshots (for the view/API layer) ----

// SnapshotWorkspaces returns immutable copies of all workspaces, tabs and
// panes (with agent snapshots), safe to read without further locking.
func (s *Server) SnapshotWorkspaces() []WorkspaceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WorkspaceInfo, 0, len(s.Workspaces))
	for _, w := range s.Workspaces {
		wi := WorkspaceInfo{WorkspaceID: w.ID, Name: w.Name, Tabs: len(w.Tabs)}
		out = append(out, wi)
	}
	return out
}

// SnapshotPanes returns snapshots of every pane in a workspace.
func (s *Server) SnapshotPanes(workspaceID string) []PaneInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PaneInfo{}
	for _, w := range s.Workspaces {
		if w.ID == workspaceID || w.Name == workspaceID {
			for _, t := range w.Tabs {
				for _, p := range t.Panes {
					out = append(out, snapshotPane(p))
				}
			}
			return out
		}
	}
	return nil
}

// SnapshotTabs returns snapshots of every tab in a workspace.
func (s *Server) SnapshotTabs(workspaceID string) []TabInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []TabInfo{}
	for _, w := range s.Workspaces {
		if w.ID == workspaceID || w.Name == workspaceID {
			for _, t := range w.Tabs {
				out = append(out, TabInfo{TabID: t.ID, Panes: len(t.Panes)})
			}
			return out
		}
	}
	return nil
}

// SnapshotPane returns a snapshot of one pane by id (qualified or engine id).
func (s *Server) SnapshotPane(id string) (PaneInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.panesByID[id]
	if p == nil {
		return PaneInfo{}, false
	}
	return snapshotPane(p), true
}

// SnapshotAgent returns a snapshot of one live agent by name.
func (s *Server) SnapshotAgent(name string) (AgentInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agentsByName[name]
	if a == nil {
		return AgentInfo{}, false
	}
	return snapshotAgent(a, s.paneIDForAgentLocked(a)), true
}

// PaneIDOfAgent returns the qualified pane id hosting an agent. Safe to call
// from mutation paths that hold the agent pointer.
func (s *Server) PaneIDOfAgent(a *Agent) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paneIDForAgentLocked(a)
}

// SnapshotAgents returns snapshots of all live agents.
func (s *Server) SnapshotAgents() []AgentInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentInfo, 0, len(s.agentsByName))
	paneByAgent := map[*Agent]string{}
	for _, w := range s.Workspaces {
		for _, t := range w.Tabs {
			for _, p := range t.Panes {
				if p.Agent != nil {
					paneByAgent[p.Agent] = p.ID
				}
			}
		}
	}
	for _, a := range s.agentsByName {
		out = append(out, snapshotAgent(a, paneByAgent[a]))
	}
	return out
}

func snapshotPane(p *Pane) PaneInfo {
	pi := PaneInfo{PaneID: p.ID, Name: p.Name, TabID: p.Tab.ID}
	if p.Agent != nil {
		pi.Agent = &AgentInfo{
			Name: p.Agent.Name, Kind: p.Agent.Kind,
			State: p.Agent.State, Seen: p.Agent.Seen, PaneID: p.ID,
		}
	}
	return pi
}

func snapshotAgent(a *Agent, paneID string) AgentInfo {
	return AgentInfo{Name: a.Name, Kind: a.Kind, State: a.State, Seen: a.Seen, PaneID: paneID}
}

// paneIDForAgentLocked finds the qualified pane id hosting an agent.
// Requires s.mu held by caller.
func (s *Server) paneIDForAgentLocked(a *Agent) string {
	for _, w := range s.Workspaces {
		for _, t := range w.Tabs {
			for _, p := range t.Panes {
				if p.Agent == a {
					return p.ID
				}
			}
		}
	}
	return ""
}
