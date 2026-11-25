# Recipes

Step by step, the files a common change touches. Each recipe names a
recent example to read alongside. Where a step is missed, a check usually
says so; the list is here so you don't have to find out that way.

## Add a setting

Example: `autosave` (**Settings › General › Save automatically**).

1. **The field and its default**: add it to the `Settings` interface and
   to `DEFAULT_SETTINGS` in `src/prefs/settings-types.ts`, with a doc
   comment saying what it means.
2. **Reading it back**: `normaliseSettings` in
   `src/prefs/settings-normalise.ts` starts from the defaults and keeps
   what was stored. A value that can be wrong in a way that matters (one of
   a list, a number in a range, a map) gets a line in `readSettings` that
   checks it: a rejected value takes its default and never drops the rest.
   Add a case to `settings-store.test.tsx` or `settings-file.test.ts` if
   you add a check.
3. **The row**: in the page that holds it, `src/settings/pages/*Page.tsx`
   or a section in `src/settings/sections/`, using `ToggleRow` or
   `SettingRow` from `src/settings/sections/controls.tsx`. Read it with
   `useSettings()` and change it with `setSettings({ field: value })`; the
   change reaches every window and `settings.json` by itself
   ([storage.md](storage.md#how-the-files-are-written)). Settings search
   reads the rows from the page, so the label is found without more work.
4. **Its label** in all ten locales ([below](#add-a-ui-string)).
5. **The backend**, only if it needs it: `readSetting("field")` in
   `backend/app.go` returns the stored JSON value, and `readFlag("field")`
   a boolean, whose default the backend takes from
   `src/prefs/backend-defaults.json` (which `DEFAULT_SETTINGS` reads too).
   A setting the backend acts on as it changes is a case in
   `settings_patch` (`backend/settings.go`), as `runInBackground` is. Settings
   that belong to a folder rather than the user (a plugin's values, the
   folder's conventions) go in its `.textus/settings.json` instead
   (`src/storage/project-settings.ts`).
6. **A test** where the setting changes behaviour, in the layer that
   catches it ([testing.md](testing.md)); `openApp(page, { settings: { … } })`
   seeds it in an e2e flow.

## Add a menu item and a shortcut

Example: **View › Mode › Default** (`view-mode-default`). One id names an
item everywhere; the macOS bar is native, Windows, Linux and the iPad draw
the same menu in HTML, and the phone has its own.

1. **The item**: an entry in its submenu in `src/registry/menu/menu.json`,
   the one table both menus are built from: the `id`, the locale key of
   its `label` and its English text (`en`), and as needed `keys` (a fixed,
   OS-convention key), `check` (an on/off item), `on` (only some platforms'
   native bars) or `setting` (the Settings › Shortcuts action whose key it
   shows). The macOS bar (`backend/menu.go`, `backend/menu_bar.go`) shows
   the labels the window sends (`set_menu_labels`, from
   `src/app/menu/native-menu-text.ts`), so the label is written once.
2. **What it sends**: `dispatchMenu` in `backend/menu_dispatch.go` emits
   `menu-<id>` to the focused editor window, or opens one for an item
   marked `orOpen`; an id the backend runs itself (Quit, New Window, Open
   Recent) is a case there. The app-drawn menu reaches the same
   `dispatchMenu` through `trigger_menu_item`.
3. **The menu model**: in `src/registry/menu/app-menu-model.ts`, add the id to
   `NativeMenuId`: `buildAppMenu` draws the table's items, and
   `buildMobileMenu` needs the item if the phone has it. An on/off item's
   state is sent to the backend with the other checks
   (`set_menu_checks`).
4. **What it does**: a handler in the `MenuHandlers` of
   `src/app/menu/menu-bridge.ts`, listening for `menu-<id>`, and its
   implementation where the window wires them (`src/app/useWindowWiring.ts`).
   On iOS the phone menu turns the id into the same event
   (`emitMenuEvent`, `src/platform/menu-events.ts`).
5. **A key the user can change**: add the action to `ShortcutAction` and
   its defaults per platform in `src/prefs/shortcut-defaults.ts`, handle
   it in `src/app/useGlobalShortcuts.ts`, show it on the item (`accel`),
   and give it a row in **Settings › Shortcuts**
   (`src/settings/pages/ShortcutsPage.tsx`).
6. **The key table**: `src/app/key-table.test.ts` lists every shortcut with
   who answers it on each platform (the native menu on macOS takes a key
   before the page does). Add a row; the test fails when two features claim
   one key, or a key the macOS menu takes reaches nothing. Check `Cmd` keys
   in the built app on a Mac.
7. **The command palette** lists every menu item from the model, so it
   needs nothing. A command with no menu item is `registerCommand`
   (`src/registry/commands.ts`).
8. **Strings** for the label in all ten locales, and `e2e/` coverage via
   `emit(page, "menu-<id>")`.

## Add a backend command

Example: the draft commands (`backend/drafts.go`, `drafts` in
`src/platform/ipc-files.ts`).

1. **The Go handler**, registered in an `init()` of the `backend/` file of
   its area (or the one that owns the state), with the command registry's
   `command` (`backend/host.go`):

   ```go
   command("save_draft_snapshot", func(c *Call, a struct {
   	TabID string  `json:"tabId"`
   	Path  *string `json:"path"`
   	…
   }) (nothing, error) {
   ```

   The arguments are a struct with the frontend's names, the answer any
   JSON value (`nothing` for none), an error `"<English> [code]"`
   (`backend/errors.go`). A type it takes or returns carries a comment
   naming its TypeScript side, and that side one naming it.

2. **Its types**: add it to its group's file in `src/platform/bindings/`,
   kept by hand since the bindings were last generated from Rust.
   `go test ./backend/` and `src/platform/commands.test.ts` fail while a
   command named there, or called by the frontend, has no handler.
3. **The client**: `npm run bindings` (`mygo generate`) writes
   `src/platform/native/host-client.ts` from `backend/host.go`; a new
   command needs it only when `Host` itself changes.
4. **The wrapper**, in the `src/platform/ipc-*.ts` file of its area:
   `command("save_draft_snapshot", tabId, path, content)` from
   `ipc-call.ts`, type-checked against the bindings. A desktop-only
   command uses `desktopQueryCommand` (a fallback on iOS) or
   `desktopActionCommand` (rejects on iOS); one that calls an AI service
   uses `aiCommand`. Callers import the area from `platform/ipc.ts`.
5. **The browser stand-in**: under `npm run dev` and in the e2e flows the
   dev shim (`src/dev/host-shim.ts`) answers a command it doesn't know
   with `null`. If the frontend needs a real answer there, add a case to
   the shim; a flow can also answer it with `mockInvoke`.
6. **Tests**: Go tests beside the file (`x.go` and `x_test.go`;
   `a.dispatch(nil, "name", args, nil)` runs a command as a window calls
   it), and the frontend's in the layer that catches its mistake.

## Add a UI string

Every string the user sees is in all ten locale files in
`src/i18n/locales/` (en, zh, zh-Hant, ja, ko, es, fr, de, ru, pt).

1. Add the key to `en.ts` beside the keys of the same feature, not at the
   end of the file. English is the source: its shape is the `Strings` type.
2. Add the same key, translated, at the same place in the nine others.
   `zh-Hant` follows the Taiwan vocabulary; the tone rules are in
   [translating.md](translating.md#tone). If you don't know a language, put
   the English text there and say so in the pull request.
3. The app's and the AI's names come from `src/i18n/names.ts` (`APP_NAME`,
   `AI_NAME`), in a template literal, never written out.
4. A count is a plural object, one entry per plural category of each
   language (`{ one: "{n} file", other: "{n} files" }`); placeholders
   (`{n}`, `{name}`, …) are kept exactly, in every language.
5. Read it as `t.key`, with `t` from `useSettings()` or passed down.
6. `npm test` runs `src/i18n/strings.test.ts`: it fails on a key missing
   from a complete language, a placeholder lost or added, a wrong plural
   form.

If the string names a control the AI should point users to, also check
`backend/ai/ui-names/*.txt` and the help guides in `src/help/`:
their tests compare them with the strings.

## Add a plugin API call

Example: `layout.panels()` and `onDidChangePanels` (commit 36709ec9), or
`textus.dates` (pure, in the runtime). The plugin author's view is
[plugins/](plugins/README.md).

1. **The runtime**: add the call to the `textus` object in its part of
   `src/plugins/host/runtime/host-api-*.js`, as `call("namespace.method", …)`
   for anything the app answers, or as code in the frame for a pure helper
   (`runtime/text.js`). An event is `on("eventName")`.
2. **The broker**: a handler named `"namespace.method"` in the
   `src/plugins/host/broker-*.ts` file of its namespace (`layoutHandlers`
   in `broker-layout.ts`, …). It checks its arguments with the helpers
   there; a call that reaches the backend goes through
   `src/platform/ipc-plugins.ts` and a Go command in
   `backend/plugin_*.go` that checks the grant again.
3. **The permission**: add the method to its permission in
   `src/plugins/api-permissions.ts`, the one table the broker and the
   install check both read. An event the window sends goes into
   `PluginEventMap` in `src/plugins/app-bridge.ts` (sent with
   `emitPluginEvent`), and its permission, or `null`, into
   `PLUGIN_EVENT_NEEDS` in `src/plugins/host/broker.ts`.
4. **The types**: declare it in `docs/dev/textus.d.ts`, with a doc comment
   ending in its "From X.Y.Z" version. `textus-types.test.ts` fails while
   the runtime and the declarations differ.
5. **The reference**: run `node scripts/plugin-reference.mjs`, which writes
   `docs/dev/plugins/reference.md` and the AI skill's copy from
   `textus.d.ts`; `plugin-reference.test.ts` fails while they are stale.
6. **Create mode's guide**: name the call in
   `backend/ai/skills/plugin.md`; `src/plugins/plugin-guide.test.ts`
   fails on a call it doesn't name.
7. **The version**: a release that adds calls raises `API_VERSION` in
   `src/plugins/host/runtime/host-core.js`, the version in `textus.d.ts`'s header, and the table in
   `docs/dev/plugins/versions.md` (`plugin-docs-facts.test.ts` holds them
   together).
8. **The browser**: if the call reaches a new backend command, the dev
   shim answers it (`src/dev/plugin-shim-api.ts` for files and the index).
9. **A test**: unit tests for the handler or helper, and an e2e flow with
   a plugin for what crosses the frame (`e2e/plugin-views.spec.ts` has
   examples; [testing.md](testing.md#the-official-plugins)).

## Add an e2e test

1. Decide it needs a browser: focus, menus, dialogs, scrolling, layout,
   the clipboard. What keys and pastes do to the document is an editor
   integration test instead ([testing.md](testing.md#which-layer-a-test-belongs-in)).
2. Put it in `e2e/<feature>.spec.ts`, named for what it shows. Open the
   window with `openApp` (or `openPhone`, or `openWithPlugins` for plugin
   flows) and seed what it needs: settings, `window.__devFs`, mocked
   commands ([the helpers](testing.md#writing-an-e2e-flow)).
3. Drive it as a user would: clicks, keys, `emit` for a menu item. Wait for
   what you assert on with `expect(...)` or `expect.poll(...)`, never a
   fixed pause; `settled` before measuring where something came to rest,
   `release` to stop a reply or a save half way.
4. A drag uses `swallowHtmlDrop` before `openApp` and `dragWithMouse`, so
   it passes only if it works in the app's window.
5. Only if WebKit behaves differently there, add the file to
   `WEBKIT_SPECS` in `playwright.config.ts`. A flow that times frames is
   tagged `@perf` and never runs in CI.
6. Run it until it is stable: `npx playwright test e2e/x.spec.ts
--repeat-each=20 --workers=4`, with `PLAYWRIGHT_PORT` set if another
   copy may be running. Check that it fails without the fix.

## Where platform-specific code goes

- **What a platform can do** is a flag in `src/platform/capabilities.ts`
  (`canUseWindows`, `hasAppMenu`, `canExportPdf`, `hasTextusAi`, …);
  callers read the flag, never the OS. A new flag gets a comment saying
  why the platform differs.
- **Asking `isIOSApp` directly** is allowed only in the files listed in
  `src/platform/ios-gates.test.ts`, each with its reason; most needs belong
  in a flag or in the phone layout instead.
- **The phone layout**: `usePhoneLayout()` (`src/engine/controls/usePhoneLayout.ts`) in
  components, and CSS in `src/mobile/styles/`, every selector starting
  with `:root[data-window-chrome="mobile"]`.
- **OS facts** (a Mac's `Cmd`, Windows' window frame) are in
  `src/platform/os.ts` (`modIsMeta`, `isWindowsPlatform`,
  `isLinuxPlatform`).
- **Go**: a file named for its OS (`x_windows.go`, `x_darwin.go`,
  `x_linux.go`) or a `//go:build` line for one OS, `runtime.GOOS` in a
  small branch; configuration per platform in `mygo.json` (`macos`,
  `windows`, `linux`), and files that ship with one platform's app in
  `resources/<platform>/`.
- **Old engines**: CSS newer than Chromium 105 goes behind `@supports`
  for WebView2 ([webview-support.md](webview-support.md)).
- **Input methods on Linux**: WebKitGTK commits a composition as
  `deleteCompositionText` then `insertFromComposition`; keep that in mind
  in `src/editor/keys/`.
- Check it on the platform: CI builds every platform, but windows, dialogs,
  drag and drop, input methods and printing are judged in the built app.
