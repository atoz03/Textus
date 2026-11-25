//go:build !linux && !windows && (!darwin || ios)

package backend

import "context"

// Elsewhere (iOS) there is no default app to set.

func editorStatus(context.Context) defaultEditorStatus { return defaultEditorStatus{} }

func makeDefaultEditor(context.Context) error { return errUnsupported("set_default_editor") }
