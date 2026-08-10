# panopticon v0.1.0

Initial production release of the **agent-aware tmux multiplexer** with a
Herdr-compatible agent contract.

Built for: `panopticon daemon` (tmux-backed, isolated per-server). The bundled
agent skill (`panopticon --skill`) lets coding agents drive panes/workspaces
with the same verbs they use for Herdr.

## Install

```bash
# macOS (Apple Silicon)
curl -L -o ~/.local/bin/panopticon \
  https://github.com/morpheus-sh/panopticon/releases/download/v0.1.0/panopticon-darwin-arm64-v0.1.0
chmod +x ~/.local/bin/panopticon

# or build from source
git clone https://github.com/morpheus-sh/panopticon.git
cd panopticon && make install
```

Requires Go ≥ 1.22 and tmux ≥ 3.2.

## Highlights

- **Agent lifecycle detection** `idle / working / blocked / done` from the
  living pane viewport — knows when an agent genuinely needs input.
- **Herdr-compatible CLI + JSON-socket API**:
  `workspace/tab/pane/agent` with `start`, `prompt --wait`, `wait`,
  `send-keys`, `read`, `split`, `run`, `wait-output`.
- **`agent prompt --wait`** with the 5-second `agent_prompt_stalled` watchdog.
- **Bundled agent skill** (`panopticon --skill`).
- **Notifications** only for `blocked` / `done` transitions, debounced
  (macOS notifications + terminal bell).
- **Persistent sessions** across daemon restarts; **graceful shutdown**;
  **concurrency-safe** (race-validated).

## Checksums (SHA-256)

```
91ba7960c6be330cdaf4c2d67bd7b73a14a50656dcd3555f0c0db2a4314893a8  panopticon-darwin-arm64-v0.1.0
8cebe9a35da95c64a7874bdc77d0d19a294bf720a2179569079eb69033f09ae4  panopticon-darwin-x86_64-v0.1.0
f3f56314e114b01a3eb91f3b0ca573f6524c437a840d9916bfe49c1f1bd65a48  panopticon-linux-amd64-v0.1.0
```

## Usage

```
panopticon daemon                                    # start the supervisor
panopticon pane split --pane w1:p1 --direction right  # create layout
panopticon agent start coder --kind claude --pane w1:p1
panopticon agent prompt coder "review the diff" --wait --timeout 120000
panopticon agent read coder --lines 120
```

## License
Apache-2.0

## Publishing the formal GitHub Release

The `v0.1.0` tag is pushed. To attach release notes + binaries as a GitHub
Release object (needs `gh` auth or a token):

```bash
gh auth login          # or: export GH_TOKEN=<token>
gh release create v0.1.0 \
  dist/panopticon-darwin-arm64-v0.1.0 \
  dist/panopticon-darwin-x86_64-v0.1.0 \
  dist/panopticon-linux-amd64-v0.1.0 \
  --title "panopticon v0.1.0" --notes "$(cat RELEASE_NOTES.md)"
```

Alternatively `goreleaser release --clean` builds + uploads everything.
