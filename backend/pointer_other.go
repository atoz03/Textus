//go:build !windows && (!darwin || ios)

package backend

// Elsewhere (Linux) nothing tells where the pointer is outside Textus's
// windows, or whether its button is down: tabs move between windows by
// Move to New Window only.

const pointerTracked = false

func leftButtonDown() bool { return false }
