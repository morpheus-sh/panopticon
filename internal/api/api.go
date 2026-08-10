// Package api implements the wire protocol agents and the CLI use to talk to
// the panopticon daemon.
//
// It mirrors Herdr's public agent-skill surface so that a skill file written
// for Herdr works against panopticon with only the binary name changed:
//
//	agent:  start, prompt, wait, get, send-keys, read
//	pane:   split, run, wait-output, read, layout
//	workspace: list, create
//	tab:    list, create
//
// The transport is JSON-lines over a unix domain socket — a minimal JSON-RPC.
// Every request carries a string "id"; the matching response echoes it, so
// concurrent callers can correlate replies. Streams are not yet a first-class
// concern (Herdr uses a richer protocol); a documented subset is fine for the
// v1 agent workflow.
package api

import (
	"encoding/json"
	"fmt"
)

// Request is the JSON-lines envelope sent by clients.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Response is the reply envelope.
type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC-style error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("api error %d: %s", e.Code, e.Message) }

// NewError builds an Error value.
func NewError(code int, msg string) *Error { return &Error{Code: code, Message: msg} }

// Success returns content marshalable into the result field.
func Success(r interface{}) Response {
	b, err := json.Marshal(r)
	if err != nil {
		return Response{Error: NewError(-32603, "marshal error: "+err.Error())}
	}
	return Response{Result: b}
}

// Failure returns a response carrying an error.
func Failure(req Request, code int, msg string) Response {
	return Response{ID: req.ID, Error: NewError(code, msg)}
}
