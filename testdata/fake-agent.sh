#!/bin/bash
# fake-agent.sh — a dumb stand-in for a coding agent so we can exercise
# panopticon's state detection without a real LLM CLI. It:
#   1. prints a banner and "thinking" markers (→ working)
#   2. produces a result (→ done)
#   3. prints an approval prompt and reads input (→ blocked)
echo "[fake-agent] Claude Code-style session starting..."
echo "● Analyzing repository..."
for i in $(seq 1 5); do
  echo "  processing chunk $i ..."
  sleep 0.4
done
echo "✓ Finished. 14 passed · 0 failed · 41.3s"
echo ""
echo "Do you want to proceed? (y/n)"
read -r ans
echo "got response: $ans"
exit 0
