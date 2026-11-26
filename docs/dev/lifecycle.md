# Launching, closing and quitting

How a Textus process starts, what keeps it running, and how it ends:
background mode and its tray icon, plugins' scheduled jobs, and updates.
The code is `backend/app.go` (`Run`: setup and MyGo's app events),
`backend/session.go` (what a launch opens, and `quit`),
`backend/windows.go` (the windows), `backend/background.go` and
`backend/tray.go`, and `backend/schedule_jobs.go`. The desktop app only;
iOS has one window that the system manages.

## Launching

A launch opens the main window, with the files named on the command line
(`textus file.md`) or else the last session's documents, when Settings
restores them (`launchOpens`).

On Windows and Linux (release builds) a second launch hands its arguments to
the running process through MyGo's single-instance lock
(`RequestSingleInstanceLock`, `OnSecondInstance`) and exits. The running
process then opens the files, or - with no file - restores the session when
no editor window is open (as a first launch would) and opens a new window
otherwise. On macOS, Finder and `open` reach the running app themselves; the
Dock icon clicked with no window open restores the session the same way
(`reopenWithoutWindows` in `windows.go`). Paths that arrive before a window
is ready wait in the app's queue (`queuePathsToOpen`) until the window asks
for them.

## Closing the last window

MyGo quits when its last window closes unless the app listens for that;
Textus listens (`lastWindowClosed`), and `keepsRunning` in `background.go`
decides:

| Platform       | Background mode off  | Background mode on             |
| -------------- | -------------------- | ------------------------------ |
| macOS          | Keeps running (Dock) | Keeps running (Dock, menu bar) |
| Windows, Linux | Exits                | Keeps running (tray)           |

Every window is destroyed when it closes; nothing is kept hidden for later.
A quit under way is never held back: its last window ends the process
(`windowGone`).

## Quitting

**File › Quit**, `⌘Q` / `Ctrl + Q` and the tray's **Quit Textus** all run
`quit` (`session.go`): it records the session, asks every window to close
(so each unsaved document gets its prompt; **Cancel** stops the quit), and
once the last window is gone ends the app (`exit`), or starts the new
version an update installed. With no window open it exits at once.

## Background mode

**Settings › General › Application › Keep running in the background**
(`runInBackground`, off by default). Turning it on makes the tray icon
(`tray.go`); turning it off removes the icon at once and, on Windows and
Linux with no window open, ends the process. Every window hears the tray's
state as `background-status`, and Settings asks for it with
`background_status`.

| Platform | Where the icon is   | Left click     | Menu        |
| -------- | ------------------- | -------------- | ----------- |
| Windows  | Notification area   | Opens Textus   | Right click |
| macOS    | Menu bar (template) | Opens the menu | Left click  |
| Linux    | StatusNotifier tray | Opens the menu | Any click   |

On Linux the panel's activation (a double click on GNOME's AppIndicator
extension) opens Textus too.

The menu: **Open Textus** (`reopenWithoutWindows`), the commands the
plugins that are on put there (`set_tray_plugin_items`), and **Quit
Textus**. Its labels are the backend's own table, in the UI language,
because the tray can be up before any window has sent labels; a test holds
them to the locale files. A plugin's command goes to the editor window
focused last, or, with none open, waits for the window that opens for it
(`take_pending_plugin_command`).

The icons are `backend/icons/tray/`, made from the app icon by
`scripts/make-tray-icons.sh`: a 36 px template image for the macOS menu bar
(macOS paints it in the bar's colour, light or dark), the app icon's
rounded square at the notification area's sizes for 100-300% scaling on
Windows (`GetSystemMetrics(SM_CXSMICON)` picks one), and a 64 px PNG on
Linux.

Linux needs libayatana-appindicator at runtime: MyGo loads it when the tray
is made, and without it the icon is not made, the process still stays, and
Settings says the tray isn't available. GNOME shows no tray icons without
the AppIndicator extension.

## Plugins' scheduled jobs

A plugin can schedule a job (`textus.schedule`; desktop only). The backend
keeps the jobs in `plugins/schedule.json` and one timer for the soonest
(`schedule_jobs.go`). A due job shows its notification from the backend
(`schedule_notify.go`) and hands its `run` to a window's plugins; with no
window open, it opens the hidden `background-host` window to run it
(`src/plugins/background/`) and closes it once the jobs have run.

## Updates

The update check runs in an editor window's frontend
(`app/app-update.ts`): at the window's start and every 4 hours while it is
open. A windowless process checks nothing, and the next window checks at
once.

The backend's `download_and_install_update` (`updater.go`) has MyGo's
updater download the update, check its signature and replace the app's
files where it is installed; the running process is not touched. Then
`quit_and_relaunch` closes every window as **Quit** does and starts the
new version. The relaunched app is a plain launch: a window, with the
session.

## What a windowless process costs

Measured on the last stable Tauri build (1.4.6) on Linux (Arch, niri on Wayland,
NVIDIA, WebKitGTK 2.50, release build, throwaway data folders, its own
D-Bus session), as the process and all its children's PSS and CPU time;
the Go build is yet to be measured the same way:

| State                                       | Processes             | PSS    | Of it private | CPU, idle 60 s |
| ------------------------------------------- | --------------------- | ------ | ------------- | -------------- |
| One editor window open, idle                | 3 (app, web, network) | 365 MB | -             | 10 ms          |
| Its window closed, with the web process fix | 2 (app, network)      | 179 MB | 51 MB (app)   | 0              |
| Its window closed, before the fix           | 3 (app, web, network) | 313 MB | -             | 20 ms          |

Most of a windowless process's PSS is shared library pages (WebKitGTK, GTK
and the GL drivers), clean and given back under memory pressure. WebKit's
network process stays once a window has been open; it has no API to end
it. The tray and its menu add no thread, timer or wakeup.

On macOS, expect the same shape: after the last window closes, the "Textus
Web Content" process should end (check in Activity Monitor), leaving the
app process and the networking one. On Windows, check in Task Manager that
the `msedgewebview2.exe` processes under Textus end within a few seconds of
the last window closing. Either way, measure the idle process for a minute
with nothing open (Activity Monitor's Memory and CPU columns, or
`Get-Process textus, msedgewebview2 | Select WS, CPU` in PowerShell before
and after) and compare with the table above.

While windows are open, what isn't in use is given back too: a document's
editor out of sight for 10 minutes is let go
([documents.md](documents.md#tabs-out-of-sight)), a window hidden or out of
focus for 10 minutes releases its caches (`src/utils/memory-idle.ts`). The
Tauri build also asked a minimized window's WebView2 to use less memory on
Windows; the Go backend doesn't yet.
