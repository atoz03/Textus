# The `textus` command

`textus` is the app's own binary, run by a small script that
`backend/cli_install*.go` installs (macOS `/usr/local/bin/textus`,
Windows `bin\textus.cmd`, Linux `~/.local/bin/textus`, which runs the
installed binary and takes the place of the link to it that MyGo's
install script makes there; a package that ships the binary itself as
`/usr/bin/textus` needs no script). The script sets `TEXTUS_CLI=1` so the
binary knows a terminal started it. The app installs it at startup where
no password is needed (a development build doesn't); Settings › General
installs it otherwise, and on Windows adds its folder to the user's PATH.

## How a command line is read

`main()` calls `backend.RunCLI` (`backend/cli_run.go`) before anything
else:

- No arguments, paths, flags or `textus://` links: the app, as before. From
  the script, the binary hands them on and returns at once (`open -a` on
  macOS, a detached process elsewhere, `cli_handoff.go`); started by the
  system, it goes on to `backend.Run`.
- A command from the table in `backend/cli/table.go`, `--help`/`-h`,
  `--version`/`-V`, or a bare word that names no file here: a command. It
  runs and the process exits before MyGo starts - no window, page or plugin
  is loaded.

A command wins over a file of the same name; `./list` opens the file.

## Where a command runs

- **In the binary** (`cli_local.go`): `help`, `version`, `list`, `read`,
  `search`, `new`, `move`. They use the AI project tools' walk, search and
  extraction, so `textus list` lists what the agent can read.
- **In the running app**: `commands`, `plugin`, `theme`, `skill`, `export`,
  and plugins' AI tools as `textus <plugin> <tool>`. The app listens as it
  starts (`cli_server.go`): a Unix socket in the app's data folder (macOS)
  or `$XDG_RUNTIME_DIR` (Linux), owner-only and checked against the peer's
  uid, or a named pipe `\\.\pipe\textus-cli-<user>` on Windows
  (`backend/cli/endpoint_*.go`). The command sends one JSON line and reads
  `{"out":…}`, `{"err":…}` and a final `{"exit":…}`
  (`backend/cli/ipc.go`). If no app answers, the command starts it and waits
  up to 30 seconds; plugin tools never start it.
- Skills and plugin tools are answered in Go: the tools come from the same
  table the agent's tools do (`plugin_ai_tools.go`), so a plugin's
  `contributes.aiTools` are terminal commands too, their options read by
  their schema (`backend/cli/toolargs.go`). The rest go to a window
  (`src/app/cli-requests.ts`), which answers with `cli_respond`.
- `export` opens the document and runs the menu's export; its save dialog
  takes the terminal's target instead of asking (`cli_export.go`,
  `export_save_dialog`), and the command waits for the file.

## Adding a command

Add a row to `Commands` in `backend/cli/table.go` (help lists it from
there), then its case in `runHere` (`cli_local.go`), or its handler in
`src/app/cli-requests.ts` for one the app answers. Output is plain text; a
command that returns data takes `--json`. Errors go to stderr with exit
code 1, usage mistakes with 2.
