#!/bin/bash
# fake-agent-plain.sh — a plain-screen stand-in for a coding agent. Prints a
# working phase, a completion frame, then a blocked approval prompt, and waits
# for a single input before exiting back to the shell. This exercises the full
# working -> done -> blocked -> (answered) -> done lifecycle on the NORMAL
# screen, so the pane returns to a shell prompt when the agent ends — which
# panopticon detects to clear the blocked state.
echo "[working] analyzing..."
sleep 2
echo "[done] 14 passed · 0 failed"
sleep 1
echo "[blocked] Do you want to proceed? (y/n)"
read -r ans
echo "got response: $ans"
exit 0
