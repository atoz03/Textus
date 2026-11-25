package backend

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"textus/backend/ai/rules"
)

// What Textus AI makes for a project folder - its memories and research
// papers - kept in Textus's own config folder, not the user's:
// `<config>/textus/projects/<key>/`. The project's .textus/ keeps only what
// the user writes (agent.md, skills, style profile, library).

// projectDataKey is a folder name for root: its path with every separator
// and odd character as '-', as Claude Code names its projects, ended by a
// short hash so two paths that read the same stay apart.
func projectDataKey(root string) string {
	readable := strings.Map(func(c rune) rune {
		if rules.IsAlphanumeric(c) {
			return c
		}
		return '-'
	}, root)
	runes := []rune(strings.Trim(readable, "-"))
	return string(runes[:min(len(runes), 80)]) + "-" + pathKey(root, 4)
}

// projectDataDir is `<global>/projects/<key>/`.
func projectDataDir(global, root string) string {
	return filepath.Join(global, projectsFolder, projectDataKey(root))
}

// adoptProjectData is sub of the project's data folder. What an older
// Textus kept in `<root>/.textus/<sub>` moves there the first time; where it
// can't be moved it is copied, and the original left as it was.
func adoptProjectData(global, root, sub string) string {
	to := filepath.Join(projectDataDir(global, root), sub)
	from := filepath.Join(projectDir(root), sub)
	if info, err := os.Stat(from); err != nil || !info.IsDir() {
		return to
	}
	if _, err := os.Stat(to); err == nil {
		return to
	}
	err := os.MkdirAll(projectDataDir(global, root), 0o755)
	if err == nil && os.Rename(from, to) != nil {
		err = copyWhole(from, to)
	}
	if err != nil {
		log.Printf("textus: could not move %s to %s: %v", from, to, err)
	}
	return to
}

// copyWhole copies the folder from to to, or leaves nothing at to when the
// copy fails. Something already at to is never copied over.
func copyWhole(from, to string) error {
	if err := os.Mkdir(to, 0o755); err != nil {
		return err
	}
	var made []string
	if err := copyTree(from, to, func(string) bool { return false }, &made); err != nil {
		undoMade(made)
		_ = os.Remove(to)
		return err
	}
	return nil
}

// windowsReservedNames are refused by Windows as file names whatever the
// extension ("con.md").
var windowsReservedNames = []string{
	"con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8",
	"com9", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
}

// safeFileStem is a file stem from text someone else chose: letters,
// digits, '-' and '_' kept, all else '-', capped at maxBytes (filesystems
// count bytes, CJK takes 3). fallback stands in for an empty stem, and is
// appended to a name Windows refuses.
func safeFileStem(name, fallback string, maxBytes int) string {
	var b strings.Builder
	for _, c := range name {
		if !rules.IsAlphanumeric(c) && c != '-' && c != '_' {
			c = '-'
		}
		if b.Len()+utf8.RuneLen(c) > maxBytes {
			break
		}
		b.WriteRune(c)
	}
	stem := strings.Trim(b.String(), "-")
	switch {
	case stem == "":
		return fallback
	case slices.Contains(windowsReservedNames, strings.ToLower(stem)):
		return stem + "-" + fallback
	}
	return stem
}

// fileStem is a file's name without its extension.
func fileStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
