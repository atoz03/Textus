package backend

import "encoding/json/jsontext"

// The commands of the subsystems this build does not have yet, and why
// each waits:
//
//   - Scheduled jobs, notifications and floating windows: plugin
//     features whose grants are the plugins'.
//   - Web page tabs: native webviews of their own, laid over a tab.
//   - The command-line tool, whose install needs the CLI mode itself, and
//     the default editor.
//   - The iOS build's own files.
//
// A command whose answer only reports what is there answers that there is
// nothing (no server, no job, no window), so the pages that ask as they
// open carry on; one that would do something fails with errUnsupported,
// which the page shows as that action failing.

// unsupportedCommands fail with errUnsupported.
var unsupportedCommands = []string{
	// floating windows, web pages
	"start_floating_tab_drag", "open_floating_window", "resize_floating_window", "floating_window_position",
	"show_floating_owner",
	"web_page_show", "web_page_navigate", "web_page_reload",
	// the command-line tool, the default editor
	"install_cli_command", "set_default_editor",
	// scheduled jobs and notifications
	"schedule_set", "notify_show",
	// the iOS build's own files (native-files.ts)
	"appearance", "begin_access", "create_document", "delete_entry", "haptic", "list_folder", "pick_document",
	"pick_folder", "read_data", "read_text", "remembered", "rename_entry", "share", "trash", "write_text",
}

// emptyAnswers are the commands that report what is there, with this
// build's answer: nothing.
var emptyAnswers = map[string]string{
	// nothing to report
	"schedule_list":              `[]`,
	"schedule_take_runs":         `[]`,
	"schedule_clear":             `false`,
	"notify_take_open":           `null`,
	"floating_desktop_supported": `false`,
	"list_window_bounds":         `[]`,
	"start_window_drag_tracking": `false`,
	"cli_command_status":         `"missing"`,
	// told, and nothing to do with it
	"background_host_idle":  `null`,
	"cli_listening":         `null`,
	"cli_respond":           `null`,
	"close_floating_window": `null`,
	"web_page_hide":         `null`,
	"web_page_close":        `null`,
}

func init() {
	for _, name := range unsupportedCommands {
		command(name, func(c *Call, _ none) (nothing, error) { return nil, errUnsupported(c.name) })
	}
	for name, answer := range emptyAnswers {
		value := jsontext.Value(answer)
		command(name, func(c *Call, _ none) (jsontext.Value, error) { return value, nil })
	}
}
