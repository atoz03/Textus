package cli

import (
	"errors"
	"io"
)

// AppID names the app's own folders; backend.Identifier is the same.
const AppID = "com.catvinci.textus"

// ErrAnswered: another Textus (a dev build beside an installed one)
// answers the command already.
var ErrAnswered = errors.New("another Textus answers the textus command")

// ErrClosed: the listener takes no more connections.
var ErrClosed = errors.New("the textus command's listener is closed")

// Conn is one connection between the command and the app.
type Conn = io.ReadWriteCloser

// Listener is where the app takes the command's connections: only this
// user's.
type Listener interface {
	Accept() (Conn, error)
	Close() error
}
