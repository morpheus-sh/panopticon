package server

import (
	"encoding/json"
	"sync"
	"testing"

	"panopticon/internal/agent"
	"panopticon/internal/api"
	"panopticon/internal/model"
)

// noopNotifier records nothing; for tests.
type noopNotifier struct{}

func (noopNotifier) Blocked(*model.Agent) {}
func (noopNotifier) Done(*model.Agent)    {}
func (noopNotifier) Stalled(*model.Agent) {}

// paramReq builds a JSON-RPC request with string-keyed params.
func paramReq(method string, kv map[string]any) api.Request {
	m := map[string]any{}
	for k, v := range kv {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return api.Request{ID: "t", Method: method, Params: b}
}

// TestServerSnapshotAPI exercises the read-only view handlers from many
// goroutines concurrently against a model that is also being mutated, and must
// be race-clean (run with -race). This guards the snapshot refactor against
// regressions into non-synchronized field reads.
func TestServerSnapshotAPI(t *testing.T) {
	s := &Server{
		model:    model.NewServer(),
		notifier: noopNotifier{},
		clfrs:    map[string]*agent.Classifier{},
		stops:    map[string]chan struct{}{},
	}
	// Seed the model directly (no engine needed for read paths).
	ws, tab := s.model.NewWorkspace("main")
	p1 := s.model.AddPane(tab, "%0", "main")
	p2 := s.model.AddPane(tab, "%1", "agent")

	// Bind a fake agent and drive its state to simulate the detector.
	a := &model.Agent{Name: "watcher", Kind: model.KindGeneric, State: model.StateWorking}
	s.model.BindAgent(p2, a)

	// A writer simulates the classification loop mutating agent state.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		states := []model.AgentState{model.StateWorking, model.StateIdle, model.StateBlocked, model.StateDone}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				s.model.SetAgentState(a, states[i%len(states)])
			}
		}
	}()

	// Concurrent readers exercising the snapshot-backed view handlers.
	var readers sync.WaitGroup
	for i := 0; i < 32; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 500; j++ {
				_ = s.WorkspaceList()
				_ = s.model.SnapshotPanes(ws.ID)
				_ = s.model.SnapshotTabs(ws.ID)
				_, _ = s.model.SnapshotPane(p1.ID)
				_, _ = s.model.SnapshotAgent("watcher")
				_ = s.model.SnapshotAgents()
				_ = s.dispatch(paramReq("agent.get", map[string]any{"name": "watcher"}))
				_ = s.dispatch(paramReq("workspace.list", nil))
				_ = s.dispatch(paramReq("pane.list", map[string]any{"workspace": ws.ID}))
				_ = s.dispatch(paramReq("tab.list", map[string]any{"workspace": ws.ID}))
			}
		}()
	}
	readers.Wait()
	close(stop)
	wg.Wait()
}
