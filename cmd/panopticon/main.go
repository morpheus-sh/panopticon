// Command panopticon is an agent-aware terminal multiplexer, built on tmux,
// that speaks Herdr's agent-skill contract. It supervises coding agents
// running in tmux panes: recognizing their lifecycle state
// (idle/working/blocked/done), exposing a JSON socket API for agent-driven
// layouts, and notifying a human only when an agent genuinely needs input.
//
// Usage is deliberately Herdr-compatible so an agent skill written for Herdr
// works with a single binary-name swap:
//
//	panopticon daemon [--server NAME]
//	panopticon agent list|get|start|prompt|wait|send-keys|read ...
//	panopticon pane list|current|split|run|read|wait-output ...
//	panopticon workspace list|current|create
//	panopticon tab list
//	panopticon --skill   # print the bundled agent skill
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"panopticon/internal/ipc"
)

// version is overridable at build time via -ldflags '-X main.version=vX.Y.Z'.
// Defaults to the in-repo version for plain `go build`.
var version = "0.1.0"

func usage() {
	fmt.Fprintf(os.Stderr, `panopticon — agent-aware tmux multiplexer (Herdr-compatible contract)

Commands:
  panopticon daemon [--server NAME]      Run the supervision daemon.
  panopticon --skill                     Print the bundled agent skill file.
  panopticon --version                   Print version.

  panopticon workspace list|current|create
  panopticon tab list --workspace <ws>
  panopticon pane list|current --workspace <ws>
  panopticon pane split --pane <id>|--current --direction right|down [--cwd D] [--no-focus]
  panopticon pane run <pane> "<cmd>"
  panopticon pane read <pane> [--lines N] [--source visible|recent|detection|scrollback]
  panopticon pane wait-output <pane> --match <text> [--regex] [--timeout MS]
  panopticon pane close <pane>

  panopticon agent list
  panopticon agent get <agent>
  panopticon agent start <name> --kind claude|codex|opencode|pi|generic --pane <id> [-- <args...>]
  panopticon agent prompt <agent> "<prompt>" [--wait] [--timeout MS]
  panopticon agent wait <agent> [--until idle|done|blocked|stalled] [--timeout MS]
  panopticon agent send-keys <agent> esc|enter|ctrl+c|...
  panopticon agent read <agent> [--lines N] [--source visible|recent|detection|scrollback]

Environment:
  PANOPTICON_SOCKET  Unix socket path for the daemon (default /tmp/panopticon/panopticon.sock).
`)
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	// Determine socket. PANOPTICON_SOCKET (injected by the daemon into panes)
	// always wins, matching Herdr's caller-context pattern. Otherwise fall
	// back to the default server-socket discovery.
	sock := os.Getenv("PANOPTICON_SOCKET")
	if sock == "" {
		sock = ipc.DefaultSocket("panopticon")
	}

	switch args[0] {
	case "--version":
		fmt.Println("panopticon " + version)
	case "--skill":
		fmt.Println(skillFile())
	case "daemon":
		runDaemon(args[1:])
	case "workspace", "tab", "pane", "agent":
		runClient(sock, args)
	default:
		usage()
	}
}

func runDaemon(argv []string) {
	serverName := "panopticon"
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--server", "-s":
			if i+1 < len(argv) {
				serverName = argv[i+1]
				i++
			}
		}
	}
	// The socket must match the default client discovery unless the daemon is
	// started in an isolated test server (--server) in which case it must not
	// collide. Honor PANOPTICON_SOCKET when explicitly set by the launcher.
	if err := serve(serverName); err != nil {
		fmt.Fprintln(os.Stderr, "panopticon:", err)
		os.Exit(1)
	}
}

func runClient(sock string, args []string) {
	if isDaemonNotRunning(sock) {
		fmt.Fprintln(os.Stderr, "panopticon: server_not_running — start it with `panopticon daemon`")
		os.Exit(1)
	}
	call := makeClient(sock)
	result, err := dispatchCLI(call, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "panopticon:", err)
		os.Exit(1)
	}
	// Pretty-print JSON result.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(result), "", "  "); err == nil {
		os.Stdout.Write(pretty.Bytes())
		os.Stdout.Write([]byte("\n"))
	} else {
		os.Stdout.WriteString(result)
		os.Stdout.Write([]byte("\n"))
	}
}
