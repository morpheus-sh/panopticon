package main

import (
	"os"
	"os/signal"
	"syscall"
)

// serve runs the daemon until it receives SIGINT or SIGTERM, then shuts down
// cleanly. User panes are left intact in tmux, so the server can be restarted
// to re-adopt the same sessions.
func serve(serverName string) error {
	srv, err := newDaemon(serverName)
	if err != nil {
		return err
	}
	// Listen first so clients can find the socket immediately, then reconcile
	// the engine.
	if err := srv.Listen(); err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	srv.Shutdown()
	return nil
}
