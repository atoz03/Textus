package backend

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Textus AI's scratch space: a folder per conversation in the system's
// temporary folder, for the files attached to it and the work the agent
// does without a project folder. Never inside the user's own folders.

// attachedFolder is the folder in a conversation's scratch space the
// attachments go to; the file tools address it as `attached/...`.
const attachedFolder = "attached"

// scratchBase is the user's own folder under the system's temporary
// folder: on Linux the runtime folder when there is one, since /tmp is
// shared between users. Tests set it.
var scratchBase = func() (string, error) {
	temp := os.TempDir()
	if runtime.GOOS == "linux" {
		if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
			if info, err := os.Stat(run); err == nil && info.IsDir() {
				temp = run
			}
		}
	}
	dir := filepath.Join(temp, "textus-agent")
	return dir, privateDir(dir)
}

// privateDir makes dir owner-only, or checks that the one there is ours and
// not a link someone planted.
func privateDir(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder.", dir)
	}
	if !ownedPrivately(info) {
		return fmt.Errorf("%s belongs to someone else.", dir)
	}
	return nil
}

// validScratchID reports whether id is safe as one folder name: what the
// frontend's conversation and paper ids look like.
func validScratchID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// conversationDir is a conversation's scratch folder, made on first use.
func conversationDir(id string) (string, error) {
	if !validScratchID(id) {
		return "", fmt.Errorf("Not a conversation id: %q", id)
	}
	base, err := scratchBase()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, id)
	return dir, privateDir(dir)
}

// attachedDir is a conversation's attachments folder, made on first use.
func attachedDir(id string) (string, error) {
	dir, err := conversationDir(id)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, attachedFolder)
	return dir, os.MkdirAll(dir, 0o755)
}

// withAttachments is the scratch folder of a conversation that has files
// attached; nothing is made.
func withAttachments(id string) (string, bool) {
	if !validScratchID(id) {
		return "", false
	}
	base, err := scratchBase()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, id)
	if info, err := os.Stat(filepath.Join(dir, attachedFolder)); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// researchDir is where research keeps a paper that has no project folder.
func researchDir() (string, error) {
	base, err := scratchBase()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "research")
	return dir, os.MkdirAll(dir, 0o755)
}
