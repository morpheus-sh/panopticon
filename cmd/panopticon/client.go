package main

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// makeClient returns a function that performs one JSON-lines RPC on the unix
// socket and decodes the result.
func makeClient(sock string) func(method string, params map[string]any, out any) error {
	return func(method string, params map[string]any, out any) error {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return errors.New("server_not_running: " + err.Error())
		}
		defer conn.Close()
		paramsJSON := map[string]any{}
		if params != nil {
			paramsJSON = params
		}
		req := map[string]any{"id": "c", "method": method, "params": paramsJSON}
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
		var resp struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			return err
		}
		if resp.Error != nil {
			return errors.New(resp.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

func isDaemonNotRunning(sock string) bool {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return true
	}
	conn.Close()
	return false
}

// dispatchCLI translates CLI subcommands into RPC calls and returns the raw
// JSON result string.
// rpcFn is the client RPC signature used throughout dispatch functions.
type rpcFn = func(method string, params map[string]any, out any) error

func dispatchCLI(call func(method string, params map[string]any, out any) error, args []string) (string, error) {
	root := args[0]
	sub := ""
	if len(args) > 1 {
		sub = args[1]
	}

	switch root {
	case "workspace":
		return dispatchWorkspace(call, sub, args[2:])
	case "tab":
		return dispatchTab(call, sub, args[2:])
	case "pane":
		return dispatchPane(call, sub, args[2:])
	case "agent":
		return dispatchAgent(call, sub, args[2:])
	default:
		return "", errors.New("unknown command group: " + root)
	}
}

func atoi(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func flagValue(args []string, names ...string) string {
	for _, n := range names {
		for i := 0; i < len(args); i++ {
			if args[i] == n && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

func hasFlag(args []string, names ...string) bool {
	for _, n := range names {
		for _, a := range args {
			if a == n {
				return true
			}
		}
	}
	return false
}

func positionalArgs(args []string, flagNames ...string) []string {
	out := []string{}
	skipNext := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if skipNext {
			skipNext = false
			continue
		}
		// A lone "--" starts the literal (native-args) region: everything after
		// is passed through verbatim, including tokens that begin with '-'
		// (they belong to the spawned agent, not to panopticon).
		if a == "--" {
			out = append(out, args[i+1:]...)
			break
		}
		// Skip a known flag and its value.
		if isFlagName(a, flagNames) {
			// If it's a value-carrying flag (has a following non-dash token),
			// consume the value too.
			skipNext = i+1 < len(args)
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Unknown flag: skip it and its value (best-effort) so it does not
			// leak into positional/literal text.
			skipNext = i+1 < len(args)
			continue
		}
		out = append(out, a)
	}
	return out
}

func isFlagName(a string, names []string) bool {
	for _, n := range names {
		if a == n {
			return true
		}
	}
	return false
}

func dispatchWorkspace(call rpcFn, sub string, args []string) (string, error) {
	switch sub {
	case "list":
		var out json.RawMessage
		if err := call("workspace.list", nil, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "current":
		p := map[string]any{}
		if name := flagValue(args, "--name"); name != "" {
			p["name"] = name
		}
		var out json.RawMessage
		if err := call("workspace.current", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "create":
		p := map[string]any{"name": flagValue(args, "--name")}
		if name := flagValue(args, "--name"); name != "" {
			p["name"] = name
		}
		var out json.RawMessage
		if err := call("workspace.create", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	default:
		return "", errors.New("workspace: unknown subcommand " + sub)
	}
}

func dispatchTab(call rpcFn, sub string, args []string) (string, error) {
	switch sub {
	case "list":
		p := map[string]any{"workspace": flagValue(args, "--workspace")}
		var out json.RawMessage
		if err := call("tab.list", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	default:
		return "", errors.New("tab: unknown subcommand " + sub)
	}
}

func dispatchPane(call rpcFn, sub string, args []string) (string, error) {
	switch sub {
	case "list":
		p := map[string]any{"workspace": flagValue(args, "--workspace")}
		var out json.RawMessage
		if err := call("pane.list", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "current":
		var out json.RawMessage
		if err := call("pane.current", map[string]any{}, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "split":
		p := map[string]any{
			"direction": flagValue(args, "--direction"),
			"cwd":       flagValue(args, "--cwd"),
			"no_focus":  hasFlag(args, "--no-focus", "--no_focus"),
		}
		if paneID := flagValue(args, "--pane"); paneID != "" {
			p["pane"] = paneID
		} else if hasFlag(args, "--current") {
			// Default to current pane = root / first.
			var cur map[string]any
			if err := call("pane.current", map[string]any{}, &cur); err == nil {
				for k, v := range cur {
					if k == "pane_id" {
						p["pane"] = v
					} else if km, ok := v.(map[string]any); ok {
						if id, ok2 := km["pane_id"]; ok2 {
							p["pane"] = id
						}
					}
				}
			}
		}
		var out json.RawMessage
		if err := call("pane.split", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "run":
		pos := positionalArgs(args)
		if len(pos) < 1 {
			return "", errors.New("pane run requires a pane id")
		}
		pane := pos[0]
		cmd := strings.TrimSpace(strings.Join(pos[1:], " "))
		if cmd == "" {
			cmd = flagValue(args, "--cmd", "--command")
		}
		var out json.RawMessage
		if err := call("pane.run", map[string]any{"pane": pane, "cmd": strings.TrimSpace(cmd)}, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "read":
		pos := positionalArgs(args)
		if len(pos) < 1 {
			return "", errors.New("pane read requires a pane id")
		}
		var out json.RawMessage
		p := map[string]any{
			"pane":  pos[0],
			"lines": atoi(flagValue(args, "--lines"), 120),
		}
		if err := call("pane.read", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "wait-output":
		pos := positionalArgs(args)
		if len(pos) < 1 {
			return "", errors.New("pane wait-output requires a pane id")
		}
		p := map[string]any{
			"pane":    pos[0],
			"match":   flagValue(args, "--match"),
			"regex":   hasFlag(args, "--regex"),
			"timeout": atoi(flagValue(args, "--timeout"), 120000),
		}
		var out json.RawMessage
		if err := call("pane.wait-output", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	default:
		return "", errors.New("pane: unknown subcommand " + sub)
	}
}

func dispatchAgent(call rpcFn, sub string, args []string) (string, error) {
	switch sub {
	case "list":
		var out json.RawMessage
		if err := call("agent.list", nil, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "get", "read", "wait", "send-keys":
		pos := positionalArgs(args)
		if len(pos) < 1 {
			return "", errors.New("agent " + sub + " requires an agent name")
		}
		name := pos[0]
		p := map[string]any{"name": name}
		var out json.RawMessage
		switch sub {
		case "get":
			if err := call("agent.get", map[string]any{"name": name}, &out); err != nil {
				return "", err
			}
		case "read":
			p["lines"] = atoi(flagValue(args, "--lines"), 120)
			if err := call("agent.read", map[string]any{"name": name, "lines": atoi(flagValue(args, "--lines"), 120)}, &out); err != nil {
				return "", err
			}
		case "wait":
			p["until"] = flagValue(args, "--until")
			p["timeout"] = atoi(flagValue(args, "--timeout"), 120000)
			if err := call("agent.wait", p, &out); err != nil {
				return "", err
			}
		case "send-keys":
			keys := strings.Join(pos[1:], " ")
			if keys == "" {
				keys = flagValue(args, "--keys", "--key")
			}
			if err := call("agent.send-keys", map[string]any{"name": name, "keys": keys}, &out); err != nil {
				return "", err
			}
		}
		return string(out), nil
	case "start":
		pos := positionalArgs(args, "--kind", "--pane", "--cwd", "--name")
		if len(pos) < 1 {
			return "", errors.New("agent start requires a name")
		}
		name := pos[0]
		p := map[string]any{
			"name":  name,
			"kind":  flagValue(args, "--kind"),
			"pane":  flagValue(args, "--pane"),
			"args":  strings.Join(pos[1:], " "),
		}
		var out json.RawMessage
		if err := call("agent.start", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	case "prompt":
		pos := positionalArgs(args, "--wait", "--until", "--timeout", "--prompt", "--text")
		if len(pos) < 1 {
			return "", errors.New("agent prompt requires an agent name")
		}
		name := pos[0]
		prompt := strings.TrimSpace(strings.Join(pos[1:], " "))
		if prompt == "" {
			prompt = flagValue(args, "--prompt", "--text")
		}
		p := map[string]any{
			"name":    name,
			"prompt":  prompt,
			"wait":    hasFlag(args, "--wait"),
			"timeout": atoi(flagValue(args, "--timeout"), 120000),
		}
		var out json.RawMessage
		if err := call("agent.prompt", p, &out); err != nil {
			return "", err
		}
		return string(out), nil
	default:
		return "", errors.New("agent: unknown subcommand " + sub)
	}
}
