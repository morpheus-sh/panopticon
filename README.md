# panopticon

An **agent-aware terminal multiplexer** for coding agents, built on tmux, that
speaks an engine-agnostic agent contract. It understands what is running inside
each pane and turns raw output streams into a semantically aware dashboard:
which agents are working, which are blocked waiting on you, which have drifted
into a hung spinner, and which finished in the background.

**The core idea:** tmux already gives you the hard 70% — PTY multiplexing,
persistent sessions that survive SSH drops and terminal closes, socket control,
scrollback. panopticon adds the 20% tmux genuinely lacks: a **semantic
supervision layer** that recognizes agent lifecycle state
(`idle` / `working` / `blocked` / `done` / `stalled`), a JSON socket API for
agent-driven layouts, and notifications that only fire when an agent genuinely
needs a human.

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
        │  • notification policy (blocked/stalled only) │
        └──────────────────────┬────────────────────────┘
                               │ tmux -L <server> (the portability seam)
                               ▼
                       ┌─────────────────┐
                       │     tmux 3.x    │  ← the engine (swappable)
                       └─────────────────┘
```

## Install

**Homebrew-style binary** (from the release assets):

```bash
curl -L -o ~/.local/bin/panopticon \
  https://github.com/morpheus-sh/panopticon/releases/download/v0.1.0/panopticon-darwin-arm64-v0.1.0
chmod +x ~/.local/bin/panopticon
```

**From source** (Go ≥ 1.22, tmux ≥ 3.2):

```bash
git clone https://github.com/morpheus-sh/panopticon.git
cd panopticon
make install          # → ~/.local/bin/panopticon
```

## Quick start

```bash
# 1. Start the supervisor daemon (owns an isolated tmux server)
panopticon daemon

# 2. In another terminal, drive it
panopticon workspace list
panopticon pane split --pane w1:p1 --direction right --cwd "$PWD"
panopticon agent start reviewer --kind claude --pane w1:p1
panopticon agent prompt reviewer "review the diff" --wait --timeout 120000
panopticon agent read reviewer --lines 120
```

TCP-free: the daemon binds a unix socket, discovered via `PANOPTICON_SOCKET`
(an env-var override, matching how caller context is injected into panes). For
throwaway isolation use `--server <name>`.

```bash
panopticon --help      # full command reference
panopticon --skill     # the agent-skill definition (engine-agnostic contract)
```

## Agent lifecycle states

| State     | Meaning                                                          | Interrupts you? |
|-----------|------------------------------------------------------------------|-----------------|
| `idle`    | ready for input                                                  | no              |
| `working` | actively producing output / mid-turn                            | no              |
| `blocked` | showing an approval / question / selector prompt                 | **yes**         |
| `stalled` | `working` but no real progress (hung spinner / LLM call)         | **yes**         |
| `done`    | a background turn completed unseen                              | yes (quiet)     |
| `unknown` | agent present but not yet classified                            | no              |

Detection runs on the **living visible viewport** of each pane, not full
scrollback — so a real block prompt is not confused with the shell echo of a
command that merely *contains* those words. Spinner frames that redraw in place
via carriage returns are recognized as non-progress, so a hung agent surfaces
as `stalled`.

## Command reference

```
panopticon daemon [--server NAME]

panopticon workspace list|current|create [--name N]
panopticon tab     list --workspace <ws>
panopticon pane    list|current [--workspace <ws>]
panopticon pane    split --pane <id> --direction right|down [--cwd D] [--no-focus]
panopticon pane    run <pane> "<cmd>"
panopticon pane    read <pane> [--lines N] [--source visible|recent|detection|scrollback]
panopticon pane    wait-output <pane> --match <text> [--regex] [--timeout MS]
panopticon pane    close <pane>

panopticon agent   list
panopticon agent   get <agent>
panopticon agent   start <name> --kind claude|codex|opencode|pi|generic --pane <id> [-- <args...>]
panopticon agent   prompt <agent> "<prompt>" [--wait] [--timeout MS]
panopticon agent   wait <agent> [--until idle|done|blocked|stalled] [--timeout MS]
panopticon agent   send-keys <agent> esc|enter|ctrl+c|...
panopticon agent   read <agent> [--lines N] [--source visible|recent|detection|scrollback]
```

`agent prompt --wait` includes Herdr's 5-second `agent_prompt_stalled`
watchdog: a prompt sent from a non-working state must produce a lifecycle change
within five seconds, else the wait returns `agent_prompt_stalled` instead of
hanging.

## The engine-agnostic (anti-lock-in) design

The agent-facing contract — CLI verbs, JSON-RPC methods, the `w1:t1:p1` ID
model, and the `idle/working/blocked/done/stalled` lifecycle — is implemented
behind a single [engine interface](internal/engine/engine.go). The tmux backend
is one implementation. Swap the backend and you rewrite one file; the model,
detection, API and agent skill stay identical. The bundled agent skill
(`panopticon --skill`) means a coding agent describes panopticon with the same
mental model it has for any other agent multiplexer.

## Project layout

```
cmd/panopticon/        CLI entry: daemon, client, embedded skill
internal/api/          JSON-lines JSON-RPC envelope (per-request id)
internal/engine/       Engine interface + tmux backend  ← THE PORTABILITY SEAM
internal/agent/        State classifier (blocked/stalled/done/working detection)
internal/ipc/          Socket path resolution (daemon+client agree)
internal/model/        Domain types + concurrency-safe snapshots
internal/notify/       Notification policy (blocked/stalled/done, debounced)
internal/server/       Daemon: engine reconciliation, socket dispatch, handlers
```

## Development

```bash
make build          # compile
make test           # unit tests
make test-race      # unit tests under the race detector
make integration    # end-to-end smoke test against a real tmux server
make vet            # static analysis
make ci             # everything CI runs, locally
make install        # install to ~/.local/bin
```

## CI

GitHub Actions runs on every push/PR to `main`:

- **unit tests** across Go 1.22 / 1.23 / 1.24 with the race detector,
  `go vet`, and a `gofmt` check
- **integration** against a real tmux backend on Ubuntu and macOS (the same
  `integration.sh` suite)
- **release artifacts**: build-only Linux binaries for `main` (uploaded as a
  CI artifact; attach them to a GitHub Release with `gh release create`)

If CI is blocked on an Apple/`osascript` notification path, it is a host
notification limitation, not a test failure — the integration suite does not
exercise OS notifications.


## License

Apache-2.0 — an open, portable contract.
