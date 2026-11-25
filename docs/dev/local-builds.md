# Faster local builds

Working in many checkouts at once - git worktrees, one per branch or per
AI agent - costs little for the Go backend, whose build cache belongs to
the user rather than the checkout. What remains is that `npm run check`
runs every unit test whatever you changed, and that has an opt-in
shortcut. Neither changes CI or the release build.

Measured on Linux (16 cores, other builds running, so a quiet machine is
faster), in a fresh copy of `main` on 2026-10-20:

| Step                                         | Time    |
| -------------------------------------------- | ------- |
| `npm ci` (packages already in the npm cache) | 2 s     |
| `npm run check`                              | 68-77 s |
| of which `npm test`                          | 30 s    |

## The Go build cache

Go keeps compiled packages in one cache per user (`go env GOCACHE`:
`~/.cache/go-build` on Linux, `~/Library/Caches/go-build` on macOS,
`%LocalAppData%\go-build` on Windows) and the modules in another
(`go env GOMODCACHE`), both keyed by content, so every worktree shares
them with nothing to set: a second worktree builds only what differs.
Measured on Linux (192 cores) on 2026-11-25: `go build` with an empty
cache took 17 s and left 196 MB in it; after a change to one file of
`backend/`, 3 s; with nothing changed, under half a second.

- Two worktrees building at the same moment share the cache safely.
- `go clean -cache` empties it; the next build fills it again.

## `npm run check:quick`

```sh
npm run check:quick                  # against origin/main
node scripts/check-quick.mjs --base HEAD~3   # against another commit
```

Runs the whole typecheck, then ESLint, Prettier and the unit tests for only
the files your branch changed since it left `origin/main`, committed or
not. The tests are the ones `vitest related` finds: changed test files and
the tests that import a changed file. The save round trip, which takes half
of `npm test`, runs only when the change can reach saving (the same rule as
CI, `reachesSaving` in `scripts/ci-changes.mjs`).

A change to one component took 13 s, one to a locale file 16 s (59 test
files import it), against about 70 s for `npm run check`; a change to the
editor or to `docs/` runs the round trip and took 52 s.

It's for the loop while you work. Run `npm run check` (or
`npm run check:all`) before you open a pull request: it also catches a test
that breaks without importing what you changed, and runs the README,
installer and updater checks.

## Installers

`npx mygo build` makes the app and its packages, for Linux by default:
`dist-app/linux-amd64/` gets the executable, a `.tar.gz` and the script
that installs it in `~/.local`, a `.deb`, an `.rpm` and a PKGBUILD. `-platform` picks
others: `windows/amd64` makes `Textus.exe` and
`textus-setup-<version>-amd64.exe` (which needs NSIS on Linux and macOS;
`-skip-installer` leaves it out), `darwin/arm64` `Textus.app` and, on a
Mac, a `.dmg` (`-skip-dmg` leaves it out). MyGo needs no cgo, so every
platform builds on any machine; signing and disk images need a Mac.
`-debug` keeps the web inspector. Run what you build only with throwaway
data folders ([AGENTS.md](../../AGENTS.md#safety)).

## The browser playground

`src/playground/` is a web edition of the editor alone (no AI, sidebar or
settings), served at <https://textus.md/playground/>. It reuses the editor
core (`editor/editor-extensions.ts`) without image storage, and keeps its
one document in the browser's localStorage.

```sh
npm run dev:playground     # http://localhost:1450/playground/
npm run build:playground   # dist-playground/playground/, built for /playground/
```

Its entry is `playground.html` and its Vite config
`vite.playground.config.ts`, so `npm run build` and `dist/` are unaffected.
CI builds it in the `check` job. It is deployed after each newest stable
release, not from `main` (`deploy-playground.yml`, called by `release.yml`;
[infrastructure.md](infrastructure.md#playground)).
