package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"panopticon/internal/api"
	"panopticon/internal/model"
)

// ---- serialization helpers ----

type workspaceView struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Tabs        int    `json:"tabs"`
}

type tabView struct {
	TabID string `json:"tab_id"`
	Panes int    `json:"panes"`
}

type agentView struct {
	Name   string           `json:"name"`
	Kind   model.AgentKind  `json:"kind"`
	State  model.AgentState `json:"state"`
	Seen   bool             `json:"seen"`
	PaneID string           `json:"pane_id,omitempty"`
}

type paneView struct {
	PaneID string     `json:"pane_id"`
	Name   string     `json:"name"`
	TabID  string     `json:"tab_id"`
	Agent  *agentView `json:"agent,omitempty"`
}

// ---- workspace handlers ----

func (s *Server) WorkspaceList() []workspaceView {
	ws := s.model.SnapshotWorkspaces()
	out := make([]workspaceView, 0, len(ws))
	for _, w := range ws {
		out = append(out, workspaceView{WorkspaceID: w.WorkspaceID, Name: w.Name, Tabs: w.Tabs})
	}
	return out
}

func (s *Server) WorkspaceCurrent(req api.Request) api.Response {
	name := paramString(req, "name")
	if name != "" {
		for _, w := range s.model.SnapshotWorkspaces() {
			if w.Name == name || w.WorkspaceID == name {
				return api.Success(workspaceView{WorkspaceID: w.WorkspaceID, Name: w.Name, Tabs: w.Tabs})
			}
		}
		return api.Failure(req, 404, "workspace not found: "+name)
	}
	ws := s.model.SnapshotWorkspaces()
	if len(ws) > 0 {
		w := ws[0]
		return api.Success(workspaceView{WorkspaceID: w.WorkspaceID, Name: w.Name, Tabs: w.Tabs})
	}
	return api.Failure(req, 404, "no workspace")
}

func (s *Server) WorkspaceCreate(req api.Request) api.Response {
	name := paramString(req, "name")
	if name == "" {
		name = "main"
	}
	ws, tab := s.model.NewWorkspace(name)

	// Engine session for the new tmux session.
	tmuxPane, err := s.eng.CreateSession(ws.Name, 200, 50)
	if err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}
	p := s.model.AddPane(tab, tmuxPane, "main")
	if err := s.attachPaneDetection(p.ID, tmuxPane); err != nil {
		return api.Failure(req, 500, "attach: "+err.Error())
	}

	return api.Success(map[string]any{
		"workspace": workspaceView{WorkspaceID: ws.ID, Name: ws.Name, Tabs: len(ws.Tabs)},
		"tab":       map[string]any{"tab_id": tab.ID, "panes": len(tab.Panes)},
		"root_pane": map[string]any{"pane_id": p.ID, "name": p.Name},
	})
}

// ---- tab / pane list ----

func (s *Server) TabList(req api.Request) api.Response {
	wID := paramString(req, "workspace")
	tabs := s.model.SnapshotTabs(wID)
	if tabs == nil {
		return api.Failure(req, 404, "workspace not found: "+wID)
	}
	out := []tabView{}
	for _, t := range tabs {
		out = append(out, tabView{TabID: t.TabID, Panes: t.Panes})
	}
	return api.Success(out)
}

func (s *Server) PaneList(req api.Request) api.Response {
	wID := paramString(req, "workspace")
	panes := s.model.SnapshotPanes(wID)
	if panes == nil {
		return api.Failure(req, 404, "workspace not found: "+wID)
	}
	out := []paneView{}
	for _, p := range panes {
		out = append(out, s.paneViewFromInfo(p))
	}
	return api.Success(out)
}

func (s *Server) PaneCurrent(req api.Request) api.Response {
	target := paramString(req, "pane")
	if target == "" {
		ws := s.model.SnapshotWorkspaces()
		if len(ws) > 0 {
			if panes := s.model.SnapshotPanes(ws[0].WorkspaceID); len(panes) > 0 {
				target = panes[0].PaneID
			}
		}
	}
	p, ok := s.model.SnapshotPane(target)
	if !ok {
		return api.Failure(req, 404, "pane not found: "+target)
	}
	return api.Success(s.paneViewFromInfo(p))
}

func (s *Server) paneViewFromInfo(p model.PaneInfo) paneView {
	v := paneView{PaneID: p.PaneID, Name: p.Name, TabID: p.TabID}
	if p.Agent != nil {
		v.Agent = &agentView{
			Name:   p.Agent.Name,
			Kind:   p.Agent.Kind,
			State:  p.Agent.State,
			Seen:   p.Agent.Seen,
			PaneID: p.Agent.PaneID,
		}
	}
	return v
}

// ---- pane operations ----

func (s *Server) PaneSplit(req api.Request) api.Response {
	target := paramString(req, "pane")
	if target == "" {
		target = paramString(req, "target")
	}
	if target == "" {
		return api.Failure(req, 400, "missing pane target")
	}
	p := s.model.ResolvePane(target)
	if p == nil {
		return api.Failure(req, 404, "pane not found: "+target)
	}
	dir := paramString(req, "direction")
	horizontal := dir == "right" || dir == "left"
	cwd := paramString(req, "cwd")
	if cwd == "" {
		cwd, _ = s.eng.WorkingDir(p.TmuxPaneID)
	}

	newTmux, err := s.eng.SplitPane(p.TmuxPaneID, horizontal, cwd)
	nopFocus := paramBool(req, "no_focus") || paramBool(req, "no-focus")
	if err != nil {
		return api.Failure(req, 500, "engine split: "+err.Error())
	}
	np := s.model.AddPane(p.Tab, newTmux, "agent")
	if err := s.attachPaneDetection(np.ID, newTmux); err != nil {
		return api.Failure(req, 500, "attach: "+err.Error())
	}
	if !nopFocus {
		_ = s.eng.Focus(np.TmuxPaneID)
	}
	return api.Success(map[string]any{
		"pane": paneView{PaneID: np.ID, Name: np.Name, TabID: np.Tab.ID},
	})
}

func (s *Server) PaneRun(req api.Request) api.Response {
	target := paramString(req, "pane")
	if target == "" {
		return api.Failure(req, 400, "missing pane")
	}
	p := s.model.ResolvePane(target)
	if p == nil {
		return api.Failure(req, 404, "pane not found: "+target)
	}
	cmd := paramString(req, "cmd")
	if cmd == "" {
		cmd = paramString(req, "command")
	}
	if cmd == "" {
		return api.Failure(req, 400, "missing cmd")
	}
	if err := s.eng.SendKeys(p.TmuxPaneID, cmd+"\n", false); err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}
	return api.Success(map[string]string{"ok": "true"})
}

func (s *Server) PaneRead(req api.Request) api.Response {
	target := paramString(req, "pane")
	if target == "" {
		return api.Failure(req, 400, "missing pane")
	}
	p := s.model.ResolvePane(target)
	if p == nil {
		return api.Failure(req, 404, "pane not found: "+target)
	}
	lines := paramInt(req, "lines", 120)
	if lines <= 0 {
		lines = 120
	}
	snap, err := s.eng.ReadScrollback(p.TmuxPaneID, lines)
	if err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}
	return api.Success(map[string]any{
		"pane":      p.ID,
		"text":      snap,
		"truncated": lines > 0 && len(snap) > lines*8, // coarse heuristic
	})
}

func (s *Server) PaneWaitOutput(req api.Request) api.Response {
	target := paramString(req, "pane")
	if target == "" {
		return api.Failure(req, 400, "missing pane")
	}
	p := s.model.ResolvePane(target)
	if p == nil {
		return api.Failure(req, 404, "pane not found: "+target)
	}
	match := paramString(req, "match")
	regex := paramBool(req, "regex")
	timeoutMS := paramInt(req, "timeout", 120000)
	if timeoutMS <= 0 {
		timeoutMS = 120000
	}
	snap, err := s.eng.WaitMatch(p.TmuxPaneID, match, regex, time.Duration(timeoutMS)*time.Millisecond)
	if err != nil {
		return api.Failure(req, 408, err.Error())
	}
	return api.Success(map[string]any{
		"pane":  p.ID,
		"match": match,
		"text":  snap,
	})
}

// ---- agent handlers ----

func (s *Server) AgentList() []agentView {
	agents := s.model.SnapshotAgents()
	out := make([]agentView, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentView{Name: a.Name, Kind: a.Kind, State: a.State, Seen: a.Seen, PaneID: a.PaneID})
	}
	return out
}

// paneOfAgent returns the qualified pane id hosting an agent by scanning live
// panes. Used only in mutation paths where a live pane is already resolved;
// the returned id is immutable after creation so this is safe.
func (s *Server) paneOfAgent(a *model.Agent) string {
	return s.model.PaneIDOfAgent(a)
}

func (s *Server) AgentGet(req api.Request) api.Response {
	target := paramString(req, "agent")
	if target == "" {
		target = paramString(req, "name")
	}
	a, ok := s.model.SnapshotAgent(target)
	if !ok {
		return api.Failure(req, 404, "agent not found: "+target)
	}
	return api.Success(agentView{Name: a.Name, Kind: a.Kind, State: a.State, Seen: a.Seen, PaneID: a.PaneID})
}

func (s *Server) AgentStart(req api.Request) api.Response {
	name := paramString(req, "name")
	paneID := paramString(req, "pane")
	kind := paramString(req, "kind")
	// Optional native args after 'args' field.
	argsStr := paramString(req, "args")

	if !model.AgentNameValid(name) {
		return api.Failure(req, 400, "invalid agent name (must match [a-z][a-z0-9_-]{0,31})")
	}
	if paneID == "" {
		return api.Failure(req, 400, "missing pane")
	}
	p := s.model.ResolvePane(paneID)
	if p == nil {
		return api.Failure(req, 404, "pane not found: "+paneID)
	}

	// Determine launch command by kind.
	launch := launchCommand(kind, argsStr)
	if launch == "" {
		return api.Failure(req, 400, "unknown agent kind: "+kind)
	}

	a := &model.Agent{Name: name, Kind: model.AgentKind(kindVal(kind)), State: model.StateWorking}
	s.model.BindAgent(p, a)

	// Reset the pane's classifier for this fresh binding so pre-binding shell
	// echo/scrollback cannot masquerade as output from the new agent.
	if clf, ok := s.clfrs[p.ID]; ok {
		clf.Bound()
	}

	if err := s.eng.SendKeys(p.TmuxPaneID, launch, true); err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}

	// Assume ready after a short settle; the classifier refines state.
	time.Sleep(800 * time.Millisecond)

	return api.Success(map[string]any{
		"agent": agentView{Name: a.Name, Kind: a.Kind, State: a.State, PaneID: p.ID},
	})
}

func launchCommand(kind, args string) string {
	bin, ok := map[string]string{
		"claude":   "claude",
		"codex":    "codex",
		"opencode": "opencode",
		"pi":       "pi",
		"agent":    "codex", // generic default
	}[kind]
	// The 'generic' / 'agent' kind runs a raw command line given in args
	// (shell): useful for supervising arbitrary long-running processes and
	// test harnesses.
	if (model.AgentKind(kind) == model.KindGeneric || kind == "generic" || kind == "any") && args != "" {
		return args
	}
	if !ok {
		return ""
	}
	if args != "" {
		return bin + " " + args
	}
	return bin
}

func kindVal(kind string) model.AgentKind {
	switch model.AgentKind(kind) {
	case model.KindClaude, model.KindCodex, model.KindOpencode, model.KindPi:
		return model.AgentKind(kind)
	}
	return model.KindGeneric
}

func (s *Server) AgentPrompt(req api.Request) api.Response {
	target := paramString(req, "agent")
	if target == "" {
		target = paramString(req, "name")
	}
	prompt := paramString(req, "prompt")
	if prompt == "" {
		prompt = paramString(req, "text")
	}
	a := s.model.ResolveAgent(target)
	if a == nil {
		return api.Failure(req, 404, "agent not found: "+target)
	}
	if prompt == "" {
		return api.Failure(req, 400, "missing prompt")
	}
	paneID := s.paneOfAgent(a)
	p := s.model.ResolvePane(paneID)
	if p == nil {
		return api.Failure(req, 404, "agent pane not found: "+paneID)
	}

	// Record whether the agent was idle at send time. Herdr's contract: a
	// prompt sent from a non-working state must produce an observed lifecycle
	// change within 5 seconds, else it is a stalled submission and we must not
	// wait indefinitely. Read through the model lock to avoid racing the
	// detector's writes.
	cur := s.model.AgentState(a)
	wasActive := cur == model.StateWorking || cur == model.StateBlocked

	if err := s.eng.SendKeys(p.TmuxPaneID, prompt+"\n", false); err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}
	a = s.markWorking(a)

	wait := paramBool(req, "wait")
	if !wait {
		return api.Success(map[string]string{"sent": "true"})
	}
	timeoutMS := paramInt(req, "timeout", 120000)
	if timeoutMS <= 0 {
		timeoutMS = 120000
	}

	// If the prompt was submitted from a non-working state, guard with the
	// 5s lifecycle-change watchdog: if no transition occurs, report stalled
	// rather than burning the full timeout.
	if !wasActive {
		hadAction, err := s.awaitAnyChange(a, 5*time.Second)
		if err != nil {
			return api.Failure(req, 408, err.Error())
		}
		if !hadAction {
			return api.Failure(req, 408, "agent_prompt_stalled: no lifecycle change within 5s after prompt")
		}
	}

	state, err := s.awaitState(a, []model.AgentState{model.StateIdle, model.StateDone, model.StateBlocked}, time.Duration(timeoutMS)*time.Millisecond)
	if err != nil {
		return api.Failure(req, 408, "agent_prompt_stalled: "+err.Error())
	}
	return api.Success(map[string]string{
		"agent": a.Name,
		"state": string(state),
	})
}

func (s *Server) markWorking(a *model.Agent) *model.Agent {
	s.model.SetAgentState(a, model.StateWorking)
	return a
}

func (s *Server) AgentSendKeys(req api.Request) api.Response {
	target := paramString(req, "agent")
	if target == "" {
		target = paramString(req, "name")
	}
	if target == "" {
		return api.Failure(req, 400, "missing agent")
	}
	a := s.model.ResolveAgent(target)
	if a == nil {
		return api.Failure(req, 404, "agent not found: "+target)
	}
	keys := paramString(req, "keys")
	if keys == "" {
		keys = paramString(req, "key")
	}
	if keys == "" {
		return api.Failure(req, 400, "missing keys")
	}
	paneID := s.paneOfAgent(a)
	p := s.model.ResolvePane(paneID)
	if p == nil {
		return api.Failure(req, 404, "agent pane not found")
	}
	// Map logical keys to tmux style keys (case-insensitive); default to raw
	// text. Because tmux send-keys treats a bare word as literal text, logical
	// key names (Enter, esc, ctrl+c, Tab, ...) must be translated to tmux's
	// key tokens exactly once.
	tmap := map[string]string{
		"esc": "Escape", "enter": "Enter", "ctrl+c": "C-c",
		"ctrl+d": "C-d", "tab": "Tab", "shift+tab": "BTab",
		"space": "Space", "up": "Up", "down": "Down",
		"left": "Left", "right": "Right", "backspace": "BSpace",
	}
	send := keys
	lk := strings.ToLower(strings.TrimSpace(keys))
	for from, to := range tmap {
		if lk == from {
			send = to
			break
		}
	}
	// If the key mapped to a tmux token, send a real keypress; otherwise treat
	// it as literal typed text.
	if isKeyToken(send) {
		if err := s.eng.SendKey(p.TmuxPaneID, send); err != nil {
			return api.Failure(req, 500, "engine: "+err.Error())
		}
	} else {
		if err := s.eng.SendKeys(p.TmuxPaneID, send, false); err != nil {
			return api.Failure(req, 500, "engine: "+err.Error())
		}
	}
	return api.Success(map[string]string{"sent": "true"})
}

// keyTokens is the set of tmux named key tokens panopticon recognizes as
// real keypresses (vs literal text). Tmux interprets capitalized names and
// C-/M-/S- modifier forms as keys; anything else is literal text.
var keyTokens = map[string]bool{
	"Enter": true, "Escape": true, "Tab": true, "BTab": true,
	"Space": true, "Backspace": true, "F1": true, "F2": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"PgUp": true, "PgDn": true, "Home": true, "End": true,
}

// isKeyToken reports whether a string is a tmux key token (a real keypress).
func isKeyToken(s string) bool {
	if keyTokens[s] {
		return true
	}
	if strings.HasPrefix(s, "C-") || strings.HasPrefix(s, "M-") || strings.HasPrefix(s, "S-") {
		return true
	}
	return false
}

func (s *Server) AgentWait(req api.Request) api.Response {
	target := paramString(req, "agent")
	if target == "" {
		target = paramString(req, "name")
	}
	if target == "" {
		return api.Failure(req, 400, "missing agent")
	}
	a := s.model.ResolveAgent(target)
	if a == nil {
		return api.Failure(req, 404, "agent not found: "+target)
	}
	until := paramString(req, "until")
	var states []model.AgentState
	switch until {
	case "blocked":
		states = []model.AgentState{model.StateBlocked}
	case "idle":
		states = []model.AgentState{model.StateIdle, model.StateDone}
	default:
		states = []model.AgentState{model.StateIdle, model.StateDone, model.StateBlocked}
	}
	timeoutMS := paramInt(req, "timeout", 120000)
	if timeoutMS <= 0 {
		timeoutMS = 120000
	}
	st, err := s.awaitState(a, states, time.Duration(timeoutMS)*time.Millisecond)
	if err != nil {
		return api.Failure(req, 408, err.Error())
	}
	return api.Success(map[string]string{"agent": a.Name, "state": string(st)})
}

func (s *Server) AgentRead(req api.Request) api.Response {
	target := paramString(req, "agent")
	if target == "" {
		target = paramString(req, "name")
	}
	if target == "" {
		return api.Failure(req, 400, "missing agent")
	}
	a := s.model.ResolveAgent(target)
	if a == nil {
		return api.Failure(req, 404, "agent not found: "+target)
	}
	paneID := s.paneOfAgent(a)
	p := s.model.ResolvePane(paneID)
	if p == nil {
		return api.Failure(req, 404, "agent pane not found")
	}
	lines := paramInt(req, "lines", 120)
	if lines <= 0 {
		lines = 120
	}
	snap, err := s.eng.ReadScrollback(p.TmuxPaneID, lines)
	if err != nil {
		return api.Failure(req, 500, "engine: "+err.Error())
	}
	return api.Success(map[string]any{
		"agent": a.Name,
		"text":  snap,
	})
}

// ---- param helpers ----

func paramString(req api.Request, key string) string {
	var m map[string]any
	if err := json.Unmarshal(req.Params, &m); err != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(t)
		}
	}
	return ""
}

func paramBool(req api.Request, key string) bool {
	return paramString(req, key) == "true" || paramString(req, key) == "1"
}

func paramInt(req api.Request, key string, def int) int {
	v := paramString(req, key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
