package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStripJSONC(t *testing.T) {
	doc := readJSONObjectFrom(t, `{
	  // a comment
	  "a": "x // not a comment", /* block */
	  "b": [1, 2,],
	}`)
	if doc == nil || string(doc["a"]) != `"x // not a comment"` || string(doc["b"]) != "[1,2]" && string(doc["b"]) != "[1, 2]" {
		t.Errorf("read %v", doc)
	}
}

func readJSONObjectFrom(t *testing.T, text string) jsonDoc {
	p := filepath.Join(t.TempDir(), "x.json")
	writeFile(t, p, text)
	return readJSONObject(p)
}

func TestObsidianSettingsAreReadAsDetected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".obsidian", "app.json"), `{"useMarkdownLinks": true, "attachmentFolderPath": "./assets", "newFileLocation": "folder", "newFileFolderPath": "Inbox"}`)
	writeFile(t, filepath.Join(dir, ".obsidian", "daily-notes.json"), `{"folder": "Daily", "format": "YYYY/MM/DD"}`)
	c := conventionsOfFolder(filepath.Join(dir))
	if c.Detected == nil {
		t.Fatal("no detected conventions")
	}
	d := *c.Detected
	if d.LinkStyle == nil || d.LinkStyle.Value != "markdown" || d.LinkStyle.App != "obsidian" {
		t.Errorf("link style %+v", d.LinkStyle)
	}
	if d.Attachments == nil || d.Attachments.Value != "./assets" {
		t.Errorf("attachments %+v", d.Attachments)
	}
	if d.NewNotes == nil || d.NewNotes.Value != "Inbox" {
		t.Errorf("new notes %+v", d.NewNotes)
	}
	if d.Daily == nil || d.Daily.Value.Folder != filepath.Join(dir, "Daily") || d.Daily.Value.Format != "YYYY/MM/DD" {
		t.Errorf("daily %+v", d.Daily)
	}
	if got := c.notesRoot(); got != dir {
		t.Errorf("notes root %q", got)
	}
	// Ours are not set by another app's: they are offered as detected.
	if c.LinkStyle != nil {
		t.Errorf("own link style set: %+v", c.LinkStyle)
	}
}

func TestTextusSettingsWinAndDecide(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".obsidian", "app.json"), `{}`)
	writeFile(t, filepath.Join(dir, ".textus", "settings.json"), `{"conventions": {"linkStyle": "wiki", "templates": "tpl", "detected": "obsidian"}}`)
	c := conventionsOfFolder(dir)
	if c.LinkStyle == nil || c.LinkStyle.Value != "wiki" || c.LinkStyle.App != "textus" {
		t.Errorf("link style %+v", c.LinkStyle)
	}
	if c.Templates == nil || c.Templates.Value != filepath.Join(dir, "tpl") {
		t.Errorf("templates %+v", c.Templates)
	}
	if c.Detected != nil {
		t.Error("a decided folder is offered detected conventions")
	}
}
