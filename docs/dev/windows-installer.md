# The Windows installer

What ships on Windows, what the user is asked, and how to change any of it.
Every rule here is enforced by a file that is named, so this document can be
checked against reality.

## One artifact

Windows gets MyGo's NSIS installer, `textus-setup-X.Y.Z-amd64.exe` (and
`-arm64.exe`), which `npx mygo build` makes from `mygo.json`. Updates don't
run it: MyGo's updater replaces the app's files in place
([lifecycle.md](lifecycle.md#updates)). NSIS comes with the build on
Windows (`mygo build` downloads it); on Linux and macOS it must be
installed (`apt install nsis`), or `-skip-installer` leaves the installer
out.

The Tauri builds (up to 1.4.6, and the 1.4.7 test builds) had an NSIS
installer of their own (Tauri's template, vendored with Textus' pages), and
before that an MSI on stable tags. Neither is built any more.

## The pages, in order

MyGo's, three of them:

| #   | Page             | What it is for                                                  |
| --- | ---------------- | --------------------------------------------------------------- |
| 1   | Install location | Defaults to `%LOCALAPPDATA%\Programs\Textus`; changeable.       |
| 2   | Progress         | Copies the files, registers the file types and `textus:` links. |
| 3   | Finish           | "Create a desktop shortcut" (checked) and "Run Textus" boxes.   |

There is no license page, no per-machine install and no UAC prompt: the
install is per user, so the app can update itself. The wording is NSIS's
own and follows the system's language, in the languages MyGo bundles.

## Upgrading

The installer of a newer version offers the installed one's folder (its
`InstallLocation`, which the user may have chosen), closes a running
Textus and writes over it. In-app updates replace the same files without
the installer.

A Tauri-era install (up to 1.4.6 and the 1.4.7 test builds, in
`%LOCALAPPDATA%\Textus`) is not found by MyGo's installer, which looks for
its own uninstall entry (`…\Uninstall\com.catvinci.textus`; Tauri's was
`…\Uninstall\Textus`): installing a Go build puts a second copy in
`%LOCALAPPDATA%\Programs\Textus`, beside the old one and its own entry in
Settings › Apps. The user's settings and documents are kept either way
([storage.md](storage.md)).

## File associations

Textus registers itself for `.md`, `.markdown` and `.mdx` per user, under
`HKEY_CURRENT_USER`, like the rest of the install. Two things do it:

- `fileAssociations` in `mygo.json`: MyGo's installer gives each extension
  a ProgID of its own (`com.catvinci.textus.md`, …) with the document's
  name, the app's icon and its open command, and names it in the
  extension's `OpenWithProgids`; the `textus:` scheme of `urlSchemes`
  likewise.
- `packaging/windows/installer.nsh` (`windows.installerInclude`), its
  `customInstall` and `customUnInstall` macros: everything that names
  Textus in a value of its own, so that another installer taking the
  default leaves Textus under **Open with** and in **Default apps**, and
  the document icon (`resources/windows/doc.ico`, installed beside
  `Textus.exe`).

| Key under `HKCU\Software`                        | Value                                                |
| ------------------------------------------------ | ---------------------------------------------------- |
| `Classes\.md\OpenWithProgids` (and `.markdown`)  | `Textus.Markdown`, empty (`REG_NONE`)                |
| `Classes\Textus.Markdown`                        | description, `DefaultIcon` (`doc.ico`), open command |
| `Classes\Applications\Textus.exe`                | `FriendlyAppName`, `shell\open\command`              |
| `Classes\Applications\Textus.exe\SupportedTypes` | `.md`, `.markdown`                                   |
| `Textus\Capabilities`                            | `ApplicationName`, `ApplicationDescription`          |
| `Textus\Capabilities\FileAssociations`           | `.md`, `.markdown` = `Textus.Markdown`               |
| `RegisteredApplications`                         | `Textus` = `Software\Textus\Capabilities`            |

The ProgID was `Markdown` up to 1.1.0-rc.6. That is the name other editors
use too - MarkText's installer writes its own command into it - so
installing one of them took `.md` from Textus entirely, "Open with"
included (#183). The uninstaller removes an old `Markdown` key while it
still opens this copy of Textus.

The uninstaller removes all of the above except the extensions' keys
themselves, which other apps share.

**Settings › General › Default Markdown editor** writes the same values
again (`backend/default_editor.go`, `windowsRegistryValues`; a change to
one needs the other) and opens **Default apps** at Textus
(`ms-settings:defaultapps?registeredAppUser=Textus`). Windows does not let
an app make itself the default - the user's choice is a hashed `UserChoice`
only the Settings app writes - so the user picks Textus there.

## The `textus` command

Textus installs it itself on startup, and again from **Settings › General**
(`backend/cli_install_windows.go`): `bin\textus.cmd` beside `Textus.exe`, and
that folder on the user's `PATH` (`HKCU\Environment`). The script sets
`TEXTUS_CLI=1` and runs `"%~dp0..\Textus.exe" %*` without `start`, so a
command's output and exit code reach the terminal; given paths or nothing,
the exe starts the window as a detached process and returns at once. A
release exe has no console of its own, so in command mode it attaches to
the terminal's (`AttachConsole`). See [cli.md](cli.md). The uninstaller
(`customUnInstall`) removes both, unless the `PATH` is longer than NSIS'
strings, which would come back cut short.

## Changing things

**The pages and their wording** are MyGo's (`mygo build`'s installer
script); a change there is a change to MyGo.

**What the installer writes** beyond MyGo's own: `packaging/windows/installer.nsh`.
Keep to `HKCU` and the user's own folders, as the install is per user;
the macros run in silent installs (`/S`) too.

**The names**: `name`, `windows.displayName` and `windows.installDirName`
in `mygo.json` ([MyGo's configuration](https://github.com/egoist/mygo/blob/main/docs/configuration.md#windows)).
A new install folder applies to new installs only.

## Known rough edges

- A Tauri-era install is left beside the new one (see _Upgrading_); the
  user uninstalls it from Settings › Apps.
- The installer is not signed until the release workflow is given a
  certificate (`windows.certificate` or `windows.signCommand`); SmartScreen
  warns about an unsigned one.
