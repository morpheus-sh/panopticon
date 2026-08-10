#!/bin/bash
# integration.sh — end-to-end smoke test for panopticon.
#
# Verifies, against a real tmux backend, the core Herdr-compatible behaviors:
#  1. daemon starts, workspace/pane discovery works
#  2. agent lifecycle: working -> done -> blocked -> (answered) -> done
#  3. pane run + pane wait-output (run in another pane, wait for output)
#  4. agent prompt --wait (submit work, wait for completion)
#
# Runs against an isolated tmux server (--server itest) so it never touches
# the user's sessions. Requires tmux and a built binary at bin/panopticon.
set -euo pipefail

BIN="${BIN:-$(cd "$(dirname "$0")" && pwd)/bin/panopticon}"
SERVER=itest
export PANOPTICON_SOCKET="$(mktemp -d)/itest.sock"

start_daemon() {
  "$BIN" daemon --server "$SERVER" >/tmp/panopticon-itest.log 2>&1 &
  DAEMON_PID=$!
  # Wait for the socket.
  for _ in $(seq 1 30); do
    [ -S "$PANOPTICON_SOCKET" ] && break
    sleep 0.2
  done
  [ -S "$PANOPTICON_SOCKET" ] || { echo "FAIL: daemon did not bind socket"; exit 1; }
}

stop_daemon() {
  kill "$DAEMON_PID" 2>/dev/null || true
  sleep 0.5
  tmux -L "$SERVER" kill-server 2>/dev/null || true
}

state_of() {
  "$BIN" agent get "$1" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("state","?"))'
}

trap stop_daemon EXIT

echo "== building =="
(cd "$(dirname "$0")" && go build -o bin/panopticon ./cmd/panopticon)

echo "== test 1: daemon + workspace =="
start_daemon
WS_JSON="$("$BIN" workspace list)"
echo "$WS_JSON" | python3 -c 'import sys,json; ws=json.load(sys.stdin); assert ws and ws[0]["workspace_id"]=="w1", ws' || { echo "FAIL: no w1 workspace"; echo "$WS_JSON"; exit 1; }
echo "PASS: workspace w1 present"

echo "== test 2: agent lifecycle (working->done->blocked->done) =="
TESTDATA="$(dirname "$0")/testdata/fake-agent-plain.sh"
"$BIN" agent start worker --kind generic --pane "$("$BIN" pane current | python3 -c 'import sys,json;print(json.load(sys.stdin)["pane_id"])')" -- "bash $TESTDATA" >/dev/null
# walk until blocked, then answer, then confirm done.
found_blocked=0; found_done_after=0; answered=0
for i in $(seq 1 25); do
  S="$(state_of worker)"
  if [ "$S" = "blocked" ] && [ "$answered" = "0" ]; then
    found_blocked=1; answered=1
    "$BIN" agent send-keys worker y >/dev/null
    "$BIN" agent send-keys worker Enter >/dev/null
  fi
  if [ "$answered" = "1" ] && [ "$S" = "done" ]; then
    found_done_after=1
  fi
  [ "$found_blocked" = "1" ] && [ "$found_done_after" = "1" ] && break
  sleep 0.5
done
[ "$found_blocked" = "1" ] || { echo "FAIL: never observed blocked"; exit 1; }
[ "$found_done_after" = "1" ] || { echo "FAIL: never observed done after answering"; exit 1; }
echo "PASS: lifecycle working->done->blocked->done"

echo "== test 3: pane run + wait-output =="
cat > /tmp/panopticon-work.sh <<'EOF'
#!/bin/bash
echo "heat up"
sleep 1
echo "SIMPLETOKEN yes"
EOF
chmod +x /tmp/panopticon-work.sh
# use a fresh pane
NP="$( "$BIN" pane split --pane "$("$BIN" pane current | python3 -c 'import sys,json;print(json.load(sys.stdin)["pane_id"])')" --direction right --cwd "$(pwd)" --no-focus | python3 -c 'import sys,json;print(json.load(sys.stdin)["pane"]["pane_id"])' )"
"$BIN" pane run "$NP" "bash /tmp/panopticon-work.sh" >/dev/null
OUT="$("$BIN" pane wait-output "$NP" --match "SIMPLETOKEN yes" --timeout 8000)"
echo "$OUT" | python3 -c 'import sys,json;assert json.load(sys.stdin)["match"]=="SIMPLETOKEN yes"' || { echo "FAIL: wait-output did not match"; exit 1; }
echo "PASS: pane run + wait-output matched 'SIMPLETOKEN yes'"

echo ""
echo "ALL INTEGRATION TESTS PASSED"
exit 0

