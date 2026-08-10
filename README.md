# panopticon

An **agent-aware terminal multiplexer** for coding agents, built on tmux, that
speaks the same agent-skill contract as Herdr — so a skill file written for one
works with the other with only a binary-name swap.

**The core idea:** tmux already gives you the hard 70% (PTY multiplexing,
persistent sessions that survive SSH drops and terminal closes, socket control,
scrollback). panopticon adds the 20% Herdr is famous for that tmux genuinely
lacks — a **semantic supervision layer**: recognizing agent lifecycle state
(`idle` / `working` / `blocked` / `done`), a JSON socket API for agent-driven
layouts, and notifications that only fire when an agent genuinely needs a
human.

```
┌──────────────────────────────────────────────────────────────┐
│                    your terminal (TUI-free CLI)               │
└──────────────────────────────────────────────────────────────┘
                              │ JSON-lines over unix socket
                              ▼
        ┌───────────────────────────────────────────────┐
        │  panopticon daemon (the supervision brain)    │
        │  • workspace/tab/pane model (w1, w1:t1, w1:p1)│
        │  • per-agent state classifier                 │
        │  • notification policy (blocked/done only)    │
        └──────────────────────┬────────────────────────┘
                               │ tmux -L <server> (the portability seam)
                               ▼
                       ┌─────────────────┐
                       │     tmux 3.x    │  ← the engine (swappable)
                       └─────────────────┘
```

## Why this de-risks the "vendor lock-in" concern

Herdr is Apache-2.0 and open source — so the lock-in is not *legal*, it is
*behavioral*: your agent skills, layout scripts, and workflow all target Herdr's
protocol. panopticon addresses the *behavioral* lock:

1. **The contract is the product, not the engine.** All agent-facing surface
   (the CLI verbs, the JSON-RPC methods, the `w1:t1:p1` ID model, the
   `idle/working/blocked/done` lifecycle) is implemented behind a single
   [engine interface](internal/engine/engine.go). The tmux backend is one
   implementation. If you ever want a different engine, you rewrite one file —
   the model, detection, API and skill stay identical.
2. **Bundled agent skill** (`panopticon --skill`) is in the same format as
   Herdr's, so a coding agent describes panopticon the same way it describes
   Herdr.

## Build

```bash
go build -o bin/panopticon ./cmd/panopticon
```

Requires Go ≥ 1.22 and tmux ≥ 3.2 on the host.

## Run

```bash
# 1. Start the daemon (owns an isolated tmux server named "panopticon")
panopticon daemon

# 2. In another terminal, drive it
panopticon workspace list
panopticon pane split --pane w1:p1 --direction right --cwd "$PWD"
panopticon agent start reviewer --kind claude --pane w1:p1 -- <agent args...>
panopticon agent prompt reviewer "review the diff" --wait --timeout 120000
panopticon agent read reviewer --lines 120
```

TCP-free: the daemon binds a unix socket, discovered via `PANOPTICON_SOCKET`
(env-var override, matching Herdr's caller-context injection). Isolation: use
`--server <name>` for throwaway test servers.

### Help

```bash
panopticon --help
panopticon --skill     # the agent skill definition
```

## Agent lifecycle states

| State     | Meaning                                                    | Interrupts a human? |
|-----------|-------------------------------------------------------------|---------------------|
| `idle`    | ready for input                                            | no                  |
| `working` | actively producing output / mid-turn                      | no                  |
| `blocked` | showing an approval / question / selector prompt           | **yes**             |
| `done`    | a background turn completed unseen                         | yes (quiet)         |
| `unknown` | agent present but unclassifiable                           | no                  |

Detection runs on the **living visible viewport** of each pane, not full
scrollback — so a real block prompt is not confused with the shell echo of a
command that merely *contains* those words (see the alt-screen heuristic; real
agents paint on the alternate screen).

## Project layout

```
cmd/panopticon/        CLI entry: daemon, client, embedded skill
internal/api/          JSON-lines JSON-RPC envelope (per-request id)
internal/engine/       Engine interface + tmux backend  ← THE PORTABILITY SEAM
internal/agent/        State classifier (blocks/done/working detection)
internal/ipc/          Socket path resolution (daemon+client agree)
internal/model/        Domain types: workspace/tab/pane/agent + ID scheme
internal/notify/       Notification policy (blocked/done only, debounced)
internal/server/       Daemon: engine reconciliation, socket dispatch, handlers
```

## Roadmap (honest)

Already working:
- ✅ tmux-backed engine seam with persistent sessions
- ✅ `workspace/tab/pane/agent` CLI + JSON-RPC surface, Herdr-compatible verbs
- ✅ agent state classifier (working/done/blocked) on visible viewport
- ✅ `pane split/run/read/wait-output`, `agent start/prompt/wait/send-keys`
- ✅ bundled agent skill (`--skill`)
- ✅ notification policy for blocked/done

Next (in rough priority):
- [ ] `agent prompt --wait` 5-second `agent_prompt_stalled` watchdog — DONE
- [ ] PID capture per pane → real process detection (not just text)
- [ ] spinner-stall detection (agent "working" but no token growth = stalled)
- [ ] layout snapshot / resume across daemon restarts (tmux sessions persist; wiring resume is in progress)
- [ ] richer read sources (`visible`, `recent`, `detection`) to match Herdr

## License

Apache-2.0, matching Herdr's license — an open, portable contract.
