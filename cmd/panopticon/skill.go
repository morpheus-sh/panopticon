package main

// skillFile returns the bundled agent skill, written in the Herdr-compatible
// format so that a coding agent can interact with panopticon through the same
// mental model it has for Herdr. Agents inside panopticon panes see the env
// vars PANOPTICON_SOCKET and PANOPTICON_WORKSPACE/TAB/PANE set by the daemon.
func skillFile() string {
	return `---
name: panopticon
description: "Control panopticon, an agent-aware terminal multiplexer (tmux-backed, Herdr-compatible). Use when the user explicitly mentions panopticon or asks to inspect/control panes, tabs, workspaces, or another agent. Not for ordinary background terminals unless orchestrating one. Requires PANOPTICON_SOCKET to be set."
---

# panopticon

panopticon organizes terminals into workspaces, tabs and panes, recognizes coding agents running inside panes, and exposes the current session through the ` + "`panopticon`" + ` CLI (Herdr-compatible syntax).

Before issuing a control command, verify you are inside a panopticon-managed pane:

    test -n "${PANOPTICON_SOCKET:-}"

If that fails, say you are not running inside panopticon and stop.

## Learn the current CLI

The installed binary is the authority for command syntax:

    panopticon --help
    panopticon workspace
    panopticon tab
    panopticon pane
    panopticon agent

Discovery commands return JSON. Read identifiers and state from those responses instead of predicting them.

## Layout, panes and agents

- Workspace, tab and pane topology organize terminal locations.
- Pane commands control raw terminals, shells, tests, servers, input and output.
- Agent commands control the recognized coding agent currently occupying a pane.

A pane exists whether or not it contains an agent. ` + "`agent start`" + ` requires an existing available shell pane and never creates, splits or moves layout.

Agent commands accept either a unique live agent name or the pane ID hosting that agent. Names match ` + "`[a-z][a-z0-9_-]{0,31}`" + `.

Agent lifecycle states: ` + "`idle`" + ` (ready for input), ` + "`working`" + ` (actively producing output), ` + "`blocked`" + ` (waiting for an approval/question prompt — interruptible), ` + "`done`" + ` (settled idle after unseen background work), ` + "`unknown`" + ` (agent present, unclassifiable).

## IDs and caller context

Public IDs are stable, opaque handles:

- workspace: ` + "`w1`" + `
- tab: ` + "`w1:t1`" + `
- pane: ` + "`w1:p1`" + `

panopticon injects caller context into each managed pane:

    printf '%s\n' "$PANOPTICON_WORKSPACE_ID" "$PANOPTICON_TAB_ID" "$PANOPTICON_PANE_ID"

Discover live state:

    panopticon workspace list
    panopticon tab list --workspace "$PANOPTICON_WORKSPACE_ID"
    panopticon pane current
    panopticon pane list --workspace "$PANOPTICON_WORKSPACE_ID"
    panopticon agent list

## Starting and coordinating an agent

Default to a sibling pane in the current tab and the current working directory. Split a wide pane right, a narrow/tall pane down:

    panopticon pane split --pane "$PANOPTICON_PANE_ID" --direction right --cwd "$PWD" --no-focus

Read the new pane id from ` + "`.result.pane.pane_id`" + `. Then start a supported agent:

    panopticon agent start reviewer --kind codex --pane <pane-id>

Submit work through the agent surface:

    panopticon agent prompt reviewer "Review the current diff and report actionable findings." --wait --timeout 120000

` + "`agent prompt --wait`" + ` waits for the first settled ` + "`idle`" + `/` + "`done`" + `/` + "`blocked`" + ` state. A prompt sent from a non-working state must produce an observed lifecycle change within five seconds, else it reports ` + "`agent_prompt_stalled`" + `.

Wait for a specific state:

    panopticon agent wait reviewer --until blocked --timeout 120000

Send logical keys:

    panopticon agent send-keys reviewer esc
    panopticon agent send-keys reviewer ctrl+c
    panopticon agent send-keys reviewer "Shift+Tab"

Inspect a result:

    panopticon agent get reviewer
    panopticon agent read reviewer --lines 120

## Running an ordinary command in another pane

    panopticon pane split --pane "$PANOPTICON_PANE_ID" --direction right --cwd "$PWD" --no-focus
    panopticon pane run <pane-id> "just test"
    panopticon pane wait-output <pane-id> --match "test result" --timeout 120000
    panopticon pane read <pane-id> --lines 120

` + "`pane wait-output`" + ` searches the snapshot immediately, so existing output can match. Use ` + "`--match`" + ` for a literal substring or ` + "`--regex`" + ` for a regular expression.

## Safety and coordination rules

- Use ` + "`--no-focus`" + ` for background work unless the user asked to switch context.
- Use ` + "`--current`" + `, an explicit pane ID, or a unique agent name. Do not rely on another client's focused pane.
- Parse IDs from JSON responses. Do not derive them from sidebar order.
- Do not close workspaces, tabs or panes you did not create unless the user explicitly asked.
- Never kill the main panopticon daemon. Use a distinct ` + "`--server`" + ` name (` + "`panopticon daemon --server test`" + `) for isolated experiments.
- CLI server errors are JSON on stderr with exit status 1. Syntax errors exit with status 2.
`
}
