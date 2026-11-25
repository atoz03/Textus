package backend

// The iOS build's own commands: its document access, share sheet, haptics
// and appearance, which its native plugin answers
// (src/platform/native-files.ts and the phone's gates). No desktop page
// calls them; they are here so that every command the bindings name has a
// handler, and each fails with errUnsupported. The desktop's commands all
// have handlers of their own.

// unsupportedCommands fail with errUnsupported.
var unsupportedCommands = []string{
	"appearance", "begin_access", "create_document", "delete_entry", "haptic", "list_folder", "pick_document",
	"pick_folder", "read_data", "read_text", "remembered", "rename_entry", "share", "trash", "write_text",
}

func init() {
	for _, name := range unsupportedCommands {
		command(name, func(c *Call, _ none) (nothing, error) { return nil, errUnsupported(c.name) })
	}
}
