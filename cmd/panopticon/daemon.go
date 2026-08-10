package main

import (
	"panopticon/internal/server"
)

// newDaemon constructs the server daemon with the given tmux server name.
func newDaemon(serverName string) (*server.Server, error) {
	return server.New(server.Config{
		ServerName: serverName,
	})
}
