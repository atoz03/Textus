#!/usr/bin/env bash
# Smoke test for the built Linux app - the parts the Playwright suite
# (e2e/, a browser against the dev server) can't reach:
#
#   1. the .deb carries the app (in /opt/textus), the textus command,
#      icons and a .desktop entry that takes files and links (Exec=... %U)
#      and claims Markdown;
#   2. the release binary starts on the file it is given, and its window
#      answers the terminal's commands over the app's socket
#      (backend/cli_server.go, src/app/cli-requests.ts): the palette's
#      commands, the themes, and an export of a document;
#   3. a second launch hands its file to the first instance, which opens
#      it, and exits (MyGo's single-instance lock, release builds only),
#      one while the first is still starting included;
#   4. Quit, run from the terminal, ends the app cleanly.
#
# Usage: scripts/smoke-linux.sh [--build]
#   --build   build the app and its packages first (npx mygo build)
#
# It needs a display: a desktop session, `xvfb-run -a` (CI), or a
# headless compositor such as `mutter --headless --virtual-monitor
# 1280x800 --wayland`. Run it inside `dbus-run-session` to keep the app
# off your session bus. The app gets a throwaway HOME, XDG config, data,
# cache and state folders and its own XDG_RUNTIME_DIR, so it neither reads
# nor writes your real settings, session or recent files, and neither its
# single-instance lock nor its command socket reaches a Textus you have
# open.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
build=0
while [ $# -gt 0 ]; do
  case "$1" in
    --build) build=1 ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

pass() { printf '  \033[32mok\033[0m   %s\n' "$1"; }
fail() {
  printf '  \033[31mFAIL\033[0m %s\n' "$1"
  exit 1
}

if [ "$build" = 1 ]; then
  (cd "$root" && npx mygo build -platform linux/amd64)
fi
out="$root/dist-app/linux-amd64"
binary="$out/textus"
version="$(node -p "require('$root/package.json').version")"
# MyGo writes a pre-release's version as Debian sorts it: 1.4.7~rc.4.
deb="$out/textus_${version/-/\~}_amd64.deb"
[ -x "$binary" ] || fail "no release binary at $binary (run with --build)"
[ -f "$deb" ] || fail "no package at $deb (run with --build)"
[ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ] ||
  fail "no display: run it under xvfb-run -a, or a headless compositor"

# Short: the sockets in XDG_RUNTIME_DIR must fit in about 100 bytes. Kept
# afterwards, with the app's logs, for a look at what failed.
work="$(mktemp -d)"
pids=()
cleanup() {
  for pid in "${pids[@]}"; do kill "$pid" 2> /dev/null || true; done
  echo "the app's folders and logs: $work"
}
trap cleanup EXIT

echo "package: $(basename "$deb")"
mkdir "$work/deb"
(cd "$work/deb" && ar x "$deb" && mkdir data && tar -xf data.tar.* -C data)
data="$work/deb/data"
[ -x "$data/usr/bin/textus" ] && pass "the textus command in /usr/bin" ||
  fail "no /usr/bin/textus in the package"
desktop="$(find "$data/usr/share/applications" -name '*.desktop' | head -n1)"
[ -n "$desktop" ] || fail "no .desktop entry in the package"
grep -qE '^Exec=(/opt/textus/)?textus %U$' "$desktop" && pass ".desktop Exec takes files and links" ||
  fail ".desktop Exec line: $(grep '^Exec=' "$desktop")"
grep -q '^MimeType=.*text/markdown' "$desktop" && pass ".desktop claims text/markdown" ||
  fail ".desktop has no Markdown MimeType"
grep -q '^MimeType=.*x-scheme-handler/textus' "$desktop" && pass ".desktop claims textus: links" ||
  fail ".desktop has no textus: scheme"
grep -qx 'StartupWMClass=textus' "$desktop" && pass ".desktop StartupWMClass" ||
  fail ".desktop StartupWMClass missing"
find "$data/usr/share/icons" -name 'textus.png' | grep -q . && pass "icons installed" ||
  fail "no textus.png icons in the package"

export HOME="$work/home" XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data" \
  XDG_CACHE_HOME="$work/cache" XDG_STATE_HOME="$work/state" XDG_RUNTIME_DIR="$work/run"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME"
mkdir -m 700 "$XDG_RUNTIME_DIR"
printf '# Smoke first\n\nOpened by the smoke test.\n' > "$work/smoke-first.md"
printf '# Smoke second\n\nForwarded by a second launch.\n' > "$work/smoke-second.md"

# The terminal's command, as the textus script runs it.
textus() { TEXTUS_CLI=1 timeout 60 "$binary" "$@"; }

echo "launch: $binary"
(cd "$work" && exec "$binary" smoke-first.md > "$work/first.log" 2>&1) &
first=$!
pids+=("$first")

# A launch without a file while the first is still starting (an app icon
# clicked twice): MyGo hands it over before the app is ready, when opening
# a window for it used to panic. The first instance takes launches once it
# holds the single-instance lock and listens (MyGo's mygo-<uid>-*.sock).
for _ in $(seq 1 250); do
  compgen -G "$XDG_RUNTIME_DIR/mygo-*.sock" > /dev/null && break
  sleep 0.02
done
(cd "$work" && timeout 15 "$binary" > "$work/early.log" 2>&1) && status=0 || status=$?
[ "$status" = 0 ] && pass "a launch while the app starts hands over" ||
  fail "a launch while the app starts ended with status $status: $(tail -n5 "$work/early.log")"
sleep 1
kill -0 "$first" 2> /dev/null && pass "first instance is running" ||
  fail "the app exited at startup: $(tail -n5 "$work/first.log")"

[ "$(textus version)" = "Textus $version" ] && pass "textus version" ||
  fail "textus version said: $(textus version 2>&1 | head -n3)"

# The app listens for the terminal once it is ready (backend/cli/
# endpoint_unix.go). A command sent before then would start a second
# Textus, which hands over to this one: not what this checks.
socket="$XDG_RUNTIME_DIR/com.catvinci.textus/cli.sock"
for _ in $(seq 1 240); do
  [ -S "$socket" ] && break
  kill -0 "$first" 2> /dev/null || break
  sleep 0.25
done
[ -S "$socket" ] && pass "the app listens for the terminal" ||
  fail "no socket at $socket after 60 s: $(tail -n5 "$work/first.log")"

# Commands the window answers: the first waits for the page to load.
list="$(textus commands list --json)" ||
  fail "textus commands list: $list $(tail -n5 "$work/first.log")"
node -e 'const l = JSON.parse(process.argv[1]); process.exit(l.some((c) => c.id === "quit") ? 0 : 1)' "$list" &&
  pass "the window lists the palette's commands" ||
  fail "no quit among the palette's commands: ${list:0:300}"
themes="$(textus theme list --json)" || fail "textus theme list: $themes"
node -e 'const l = JSON.parse(process.argv[1]); process.exit(l.filter((t) => t.current === "*").length === 1 ? 0 : 1)' "$themes" &&
  pass "the window lists the themes, one of them chosen" ||
  fail "textus theme list --json: ${themes:0:300}"
textus export "$work/smoke-first.md" --to html --out "$work/exported.html" > "$work/export.log" 2>&1 ||
  fail "textus export: $(tail -n5 "$work/export.log")"
grep -q 'Smoke first' "$work/exported.html" && pass "the window exported a document to HTML" ||
  fail "the export has no 'Smoke first': $(head -c 300 "$work/exported.html")"

# A relative path: the receiving instance must resolve it against the
# second launch's working directory, not its own.
mkdir "$work/elsewhere"
mv "$work/smoke-second.md" "$work/elsewhere/"
start=$(date +%s)
(cd "$work/elsewhere" && timeout 15 "$binary" smoke-second.md > "$work/second.log" 2>&1) &&
  status=0 || status=$?
[ "$status" = 0 ] && [ $(($(date +%s) - start)) -lt 10 ] &&
  pass "second launch exited after handing over" ||
  fail "second launch did not exit promptly (status $status): $(tail -n5 "$work/second.log")"
kill -0 "$first" 2> /dev/null && pass "first instance still running" ||
  fail "the first instance ended after the second launch: $(tail -n5 "$work/first.log")"
# The window records its documents in the session as they open
# (update_window_session, backend/session.go).
session="$XDG_CONFIG_HOME/com.catvinci.textus/session.json"
for _ in $(seq 1 60); do
  grep -qF "\"$work/elsewhere/smoke-second.md\"" "$session" 2> /dev/null && break
  sleep 0.25
done
grep -qF "\"$work/elsewhere/smoke-second.md\"" "$session" 2> /dev/null &&
  pass "first instance opened the forwarded file, from the second launch's folder" ||
  fail "the session never had elsewhere/smoke-second.md: $(head -c 300 "$session" 2> /dev/null)"

# Quit asks every window to close; nothing is unsaved, so none asks.
textus commands run quit > /dev/null || fail "textus commands run quit"
for _ in $(seq 1 60); do
  kill -0 "$first" 2> /dev/null || break
  sleep 0.25
done
if kill -0 "$first" 2> /dev/null; then
  fail "the app is still running 15 s after Quit"
fi
wait "$first" && pass "Quit ended the app (exit 0)" ||
  fail "the app exited with status $? after Quit: $(tail -n5 "$work/first.log")"
if grep -E -i 'panic|fatal|segmentation' "$work/first.log"; then
  fail "the app's log has the lines above"
fi
pass "no panic in the app's log"

echo "all smoke checks passed"
