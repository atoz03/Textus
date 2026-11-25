// Package cli is what the `textus` terminal command and the running app
// share: the table of commands, the line protocol between them and where
// it runs (a Unix socket, a named pipe on Windows), and plugins' tools read
// as command-line options.
package cli

import (
	"fmt"
	"strings"
)

// Runs is where a command runs.
type Runs int

const (
	// Here: in the terminal's process, without the app.
	Here Runs = iota
	// App: in the running app, which is started first if it isn't.
	App
	// Open: handed to the app the way a file manager opens files.
	Open
	// NotYet: kept for later; says so and is left out of help.
	NotYet
)

// Command is one `textus` command.
type Command struct {
	Name, Usage, Summary string
	// Details are more lines for `textus help <command>`.
	Details string
	Runs    Runs
}

// Commands are every `textus` command, as `textus help` lists them.
var Commands = []Command{
	{Name: "open", Usage: "textus open [<file or folder>...]",
		Summary: "Open files or folders in Textus (also: textus <path>...)",
		Details: "With nothing to open, brings Textus to the front.", Runs: Open},
	{Name: "list", Usage: "textus list [<folder>] [--json]",
		Summary: "List the notes and documents in a folder",
		Details: "Lists what Textus AI can read there: text files and documents " +
			"(PDF, Word, ...), leaving out hidden and ignored files.\n" +
			"The folder is the current one when left out.", Runs: Here},
	{Name: "read", Usage: "textus read <file> [--json]",
		Summary: "Print a file as Markdown, converting PDF, Word, EPUB and more",
		Details: "Markdown and text print as they are; PDF, DOCX, PPTX, XLSX, " +
			"EPUB, RTF and HTML print as the Markdown Textus reads from them.", Runs: Here},
	{Name: "search", Usage: "textus search <query> [<folder>] [--json]",
		Summary: "Find the lines in a folder's files that contain words",
		Details: "Ignores case. Looks inside text files and documents, as " +
			"`textus list` lists them. Exits with 1 when nothing matches.", Runs: Here},
	{Name: "new", Usage: "textus new <file> [--title <title>]",
		Summary: "Create a Markdown file",
		Details: "Adds .md to a name without an extension. Never replaces a file " +
			"that is already there.", Runs: Here},
	{Name: "move", Usage: "textus move <from> <to>",
		Summary: "Move or rename a file or folder",
		Details: "Into <to> when it is a folder. Never replaces anything already " +
			"there, and doesn't move between disks.", Runs: Here},
	{Name: "export", Usage: "textus export <file> --to <format> [--out <file>]",
		Summary: "Export a document as PDF, HTML, Word and more, through Textus",
		Details: "Formats: pdf, html, docx, odt, rtf, epub, latex, mediawiki, rst, " +
			"textile, opml (all but pdf and html need pandoc).\n" +
			"Saves next to the document unless --out says where, and never " +
			"replaces a file. Opens the document in Textus to export it.", Runs: App},
	{Name: "commands", Usage: "textus commands [list | run <id>] [--json]",
		Summary: "List the command palette's commands, or run one",
		Details: "Plugins' commands included. Runs in the Textus window in front.", Runs: App},
	{Name: "plugin", Usage: "textus plugin list | install <id or path> | enable <id> | disable <id> [--json]",
		Summary: "List, install, turn on or turn off plugins",
		Details: "install takes a plugin from the store by its id, or a package or " +
			"folder by its path. Turning one on asks for its permissions in Textus.", Runs: App},
	{Name: "theme", Usage: "textus theme list | set <id> [--json]",
		Summary: "List the editor themes, or choose one", Runs: App},
	{Name: "skill", Usage: "textus skill list [--json]",
		Summary: "List Textus AI's skills", Runs: App},
	{Name: "help", Usage: "textus help [<command>]",
		Summary: "Show this list, or how to use one command", Runs: Here},
	{Name: "version", Usage: "textus version",
		Summary: "Print the version of Textus",
		Details: "Also: textus --version, textus -V", Runs: Here},
	{Name: "ai", Usage: "textus ai", Summary: "Textus AI from the terminal", Runs: NotYet},
	{Name: "research", Usage: "textus research", Summary: "Research mode from the terminal", Runs: NotYet},
}

// Find is the command named name.
func Find(name string) (Command, bool) {
	for _, c := range Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// VersionLine is what `textus version` prints.
func VersionLine(version string) string { return "Textus " + version }

// Overview is `textus help`: every command but the ones kept for later,
// then the plugins' commands, or plugins saying why there are none to show.
func Overview(version, plugins string) string {
	width := 0
	for _, c := range Commands {
		width = max(width, len(c.Name))
	}
	var b strings.Builder
	b.WriteString(VersionLine(version) + " - an Integrated Writing Environment, designed for professional writing\n\n" +
		"Usage: textus [<file or folder>...]\n" +
		"       textus <command> [<arguments>]\n" +
		"       textus <plugin> <tool> [--<name> <value>...] [--json '<object>']\n\n" +
		"Commands:\n")
	for _, c := range Commands {
		if c.Runs != NotYet {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Name, c.Summary)
		}
	}
	b.WriteString("\nPlugin commands:\n")
	b.WriteString(plugins)
	b.WriteString("\n\nRun 'textus help <command>' for one command's usage. Commands that " +
		"return data print JSON with --json.\n")
	return b.String()
}

// Usage is `textus help <command>` and `textus <command> --help`.
func Usage(c Command) string {
	out := "Usage: " + c.Usage + "\n\n" + c.Summary + ".\n"
	if c.Details != "" {
		out += "\n" + c.Details + "\n"
	}
	return out
}
