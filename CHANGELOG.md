# Changelog

All notable changes to panopticon are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — first release

Initial production release of the agent-aware tmux multiplexer with a
Herdr-compatible contract.

### Added
- **tmux-backed engine**, isolated per-server (`panopticon daemon --server`),
  with persistence across daemon restarts (sessions are re-adopted, not lost).
- **Agent lifecycle detection** (`idle` / `working` / `blocked` / `done`) from
  the living visible viewport, with:
  - approval/question/selector prompt detection → `blocked`
  - turn-completion & shell-prompt return → `done`
  - startup grace period so a freshly-launched agent (and the launch echo)
    is not misclassified
  - a shell-prompt rule that clears a stale `blocked` when an agent exits
- **Herdr-compatible CLI + JSON-socket API**:
  - `workspace list|current|create`, `tab list`, `pane list|current|split|run|read|wait-output`
  - `agent list|get|start|prompt|wait|send-keys|read`
  - `agent prompt --wait` with the 5-second `agent_prompt_stalled` watchdog
  - logical key mapping (`esc`, `enter`, `ctrl+c`, ...) to real tmux keypresses
- **Bundled agent skill** (`panopticon --skill`), same format as Herdr.
- **Notification policy** — only `blocked`/`done` transition notifications,
  debounced, with macOS `osascript` (timeout-guarded) and terminal bell.
- **Concurrency-safe model snapshots** for the view/API layer; validated under
  `-race` with a concurrent stress test.
- **Graceful shutdown** on SIGINT/SIGTERM: removes the socket, stops detection
  goroutines, leaves user panes intact for restart.
- Unit tests, race tests, and an end-to-end `integration.sh` smoke suite.
- `Makefile` targets (`build`, `test`, `test-race`, `vet`, `integration`,
  `install`) and an Apache-2.0 `LICENSE`.

### Fixed (from internal review)
- Daemon no longer dies on restart when the tmux session already exists
  (adopts the existing session).
- Removed deadlocks in the JSON socket dispatch while waiting on agent state.
- Eliminated data races in read-only view handlers by routing them through
  concurrency-safe model snapshots.
- Resolved the `agent.wait`/`agent.send-keys`/`agent.read` handlers ignoring
  the `name` parameter.
- Cleared agent-name bindings when a pane is released (prevents leaks).
- Cleaned dead code and unused model fields.
