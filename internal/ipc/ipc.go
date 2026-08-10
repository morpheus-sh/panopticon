// Package ipc centralizes socket path resolution so the daemon and every
// client agree on where the API socket lives. This prevents the classic
// "daemon bound a socket on a path the client isn't looking at" failure
// (e.g. os.TempDir() on macOS points into /var/folders/..., not /tmp).
package ipc

import (
	"os"
	"path/filepath"
)

// EnvSocketName is the env var that overrides socket discovery for a session.
const EnvSocketName = "PANOPTICON_SOCKET"

// DefaultSocket returns the socket path for a named server. The env var
// PANOPTICON_SOCKET always wins, matching Herdr's caller-context injection.
func DefaultSocket(serverName string) string {
	if p := os.Getenv(EnvSocketName); p != "" {
		return p
	}
	dir := os.Getenv("PANOPTICON_CACHE_DIR")
	if dir == "" {
		// Use the same algorithm as the server (os.TempDir()), so clients and
		// daemon agree. Relative to the home temp dir, namespaced by server.
		dir = filepath.Join(os.TempDir(), "panopticon")
	}
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, serverName+".sock")
}
