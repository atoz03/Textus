# Architecture

Where things live and which way the dependencies point. Read this before
adding a folder or an import that crosses one. Other documents go deeper:
[documents.md](documents.md) (a document from opening to closing),
[storage.md](storage.md) (what is saved where), [testing.md](testing.md)
(what each suite covers), [recipes.md](recipes.md) (adding a setting, a
menu item, a command), [ai-proposals.md](../design/ai-proposals.md) (an AI
edit from stream to document) and [plugin-api.md](plugin-api.md) (the
registries plugins add to).

Textus is a MyGo app (github.com/egoist/mygo). The frontend in `src/` is
React and TypeScript; the document is edited in Milkdown (ProseMirror), and
source mode in CodeMirror 6 (`src/editor/modes/SourceEditor.tsx`,
`DocSourceEditor.tsx`). The backend in `backend/` is Go
([The backend](#the-backend)).

## Layers

`src/` is grouped by feature, and the features are stacked in layers.
ESLint enforces the import direction (`no-restricted-imports` in
`eslint.config.js`, run by `npm run lint`); a type-only import may cross,
since it leaves nothing behind at runtime, and tests (`*.test.*`) are not
held to it.

```
shell     app/  document-view/  lazy/  playground/  help/       may import anything
features  ai/  clipboard/  document/  editor/  export/  files/
          mobile/  onboarding/  plugins/  settings/  theme/     each other, registry/, engine/ and base; never the shell
registry  registry/                                             engine/ and base
engine    engine/ (tokens, motion, frame, drag, controls, layout)   base only
base      platform/  utils/  i18n/  storage/  prefs/            each other; nothing above
-         the entry files src/*.ts(x), dev/                      not layered (below)
```

What the lint enforces:

- **Base** (`platform/`, `utils/`, `i18n/`, `storage/`, `prefs/`) imports
  no `engine/`, `registry/`, feature or shell folder. The five are one
  layer to the lint.
- **`engine/`** imports no feature, registry or shell folder; inside it
  each part uses only those before it - `tokens/`, `motion/`, `frame/`
  (the solver and the clock), `drag/`, `controls/`, `layout/` - and
  `controls/` never the layout.
- **`registry/`** holds what the shell and the features both register into
  or read: commands (`commands.ts`), the menu model (`menu/`), the fixed
  shortcuts, the sidebar tabs and context-menu items plugins add, the file
  marks a feature puts on the tree's rows. It imports only the engine and
  base, so `plugins/` and the features reach these without the shell.
- **Features** import no shell folder (`app/`, `document-view/`). They may
  import each other, with one exception: **`editor/`** never imports
  `ai/`, `document/`, `export/`, `files/`, `mobile/`, `onboarding/` or
  `plugins/`. The Markdown editor works on its own - its block model
  (`editor/markdown/doc-blocks.ts`, `doc-markdown.ts`, `edit-actions.ts`)
  included, which the AI and plugins build on; plugins attach through
  `editor/extension-points.ts`.
- **`plugins/`** is a feature like the others: what the window lends it
  comes through `plugins/app-bridge.ts` and `configurePlugins` (its
  window info: strings, keys, the command palette), set by
  `app/usePluginWindow.ts`.
- **The shell** (`app/`, `document-view/`, `lazy/`, `playground/`,
  `help/`) may import anything except the plugin system's insides.
- **Nothing outside `plugins/` imports `plugins/host/`**: the sandbox, the
  runtimes and the broker can change without the app noticing.
- **Only `src/platform/` and `src/dev/` import `mygo-runtime`**, so the
  same code runs in the browser build, where each platform module has a
  stand-in (`TEXTUS_BROWSER`, `platform/runtime.ts`).

What is only convention:

- **`lazy/` and `help/`** are linted as shell but are used from every
  layer: `lazy/lazy-module.ts` is how anything loads on first use, and
  `help/` is only the bundled guides.
- **Nothing imports `playground/`**; it is its own entry.
- The entry files: `main.tsx` picks the window to render (an editor
  window, the detached chat, a floating plugin view, the background host);
  `plugin-api.ts` is the in-process registry API (`window.__textusPlugins`
  in a development build); `test-setup.ts` is vitest's.

**The window's model has one owner**: `engine/layout/store.ts` holds the
tabs, the tab in use and the islands, and settles the layout with every
change. React reads it (`app/tabs/useTabState.ts`), the frame store keeps
only geometry, and code that needs the tab in use asks it
(`tabInUse()`, `engine/layout/tab-element.ts`), never the DOM.

The other checks `npm run check` runs (cycles, copies, dead code, code
shape) are listed in [code-style.md](code-style.md).

## UI layering

Inside the interface, a second, finer order: **tokens → styles →
components → patterns → features**. A layer never re-defines a value an
earlier one defines, and never imports from a later one. The design rules
for each are in [design-language.md](../design/design-language.md).

| Layer      | Today                                                                                              | Holds                                   |
| ---------- | -------------------------------------------------------------------------------------------------- | --------------------------------------- |
| Tokens     | `src/engine/tokens/base.css`, `design-system.css` (variables), `motion.css`                        | Role tokens and their derivation        |
| Styles     | `src/engine/tokens/design-system.css` (controls, states), `src/plugins/host/runtime/plugin-ui.css` | Control looks and states, once          |
| Components | `src/engine/controls/*.tsx` (`Select`, `Tooltip`, `ContextMenu`, `app-dialog`, `InfoTip`, `icons`) | React wrappers; no own colours or sizes |
| Patterns   | Settings rows, the sidebar, menus, the command palette                                             | Arrangements of components              |
| Features   | `src/editor`, `src/ai`, `src/files`, `src/settings`, `src/plugins`…                                | Compose the layers above                |

Held by tests: `src/engine/controls/theme-reach.test.ts`,
`engine/motion/motion-tokens.test.ts`, `motion-properties.test.ts`,
`style-invalidation.test.ts`, `e2e/a11y.spec.ts`, and the contrast tests in
`src/engine/tokens/palette.test.ts`.

Planned, not built: a layout that gives each layer a folder of its own
(`src/engine/tokens/`, `styles/`, `icons/`, `components/`, `patterns/`, with
`plugins/host/` injecting tokens, styles and icons into plugin pages),
moved file by file as files are touched; a Stylelint configuration banning
literal colours, px font sizes, off-grid spacing, raw radii and raw
durations, offered to plugin authors too; a test that every public token
resolves in every built-in theme; and a component gallery page in dev mode
that is also the visual regression target.

## The frontend's folders

| Folder           | Holds                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `app/`           | The editor window. `App.tsx` composes it from the window hooks (`useWindowShell`, `useWindowDocuments`, `useWindowWiring`, `useFileOpening`, `useSaveTabs`, `close-flow.ts`, `ClosePrompt.tsx`), `WindowBar`, `StartPage`, the command palette, global shortcuts, zoom, F6 regions (`regions.ts`), plugins' status items.                                                                                                                                                                                                                              |
| `app/tabs/`      | The tabs' state (`useTabState`), tabs that show a page - Settings, a plugin's page, Create, an AI conversation (`page-tabs.tsx`) - and tab drag between windows (`useTabDragMerge`).                                                                                                                                                                                                                                                                                                                                                                   |
| `app/frame/`     | The window's frame: the window bar (`WindowBar`), the activity rail, the sidebar with Textus's own tabs (`sidebar-tabs.tsx`; a phone pushes the same sidebar in from the left, `mobile/usePushedSidebar.ts`) and the panels' state.                                                                                                                                                                                                                                                                                                                    |
| `app/islands/`   | What this window's islands show (the layout itself is `engine/layout/`): the documents' islands, Textus AI's conversations as tabs (`useAiTabs`), a tab pulled out of its row (`tab-pull.ts`), the split saved and restored.                                                                                                                                                                                                                                                                                                                           |
| `app/menu/`      | The menu drawn (`AppMenu.tsx` and `MenuBar.tsx` on Windows, Linux and iPad, and on the phone) from the model in `registry/menu/`, native menu events (`menu-bridge.ts`), the macOS bar's labels (`native-menu-text.ts`), Open Recent (`recents.ts`).                                                                                                                                                                                                                                                                                                   |
| `app/report/`    | The error boundary and **Help › Report a Problem**.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `document-view/` | One tab's document on screen. `MilkdownEditor.tsx` is the editor with everything attached (AI panel, Quick Ask, review margin, grammar card, tutorial hooks), split into `use*.ts` hooks beside it. `editor-extensions.ts` puts the editor core and the AI plugins together. `EditorPane` switches between the rich editor, source and split view. `plugins/` is what plugins do in the editor. The editor test harnesses live here too.                                                                                                               |
| `document/`      | A document's life outside the editor ([documents.md](documents.md)): the tab model (`doc-tabs.ts`), autosave, draft recovery, reload after an outside change, the session and reload restore, read-only, line endings (`file-format.ts`), Word import, last carets, the iPhone's recent documents.                                                                                                                                                                                                                                                     |
| `editor/`        | The Markdown editor. Core plugins register in `editor-extensions.ts`, in load order. Subfolders by feature: `markdown/` (parse, save, paste, front matter, render to HTML), `marks/`, `math/`, `code/`, `tables/`, `images/`, `html/`, `find/`, `embeds/`, `chart/`, `emoji/`, `hover/`, `completion/`, `clipboard/`, `lazy/` (what is drawn only near the screen), `keys/` (key handling per platform), `modes/` (source in CodeMirror, split, typewriter, zen; `view-mode.ts` is **View › Mode**). `extension-points.ts` is where plugins add to it. |
| `ai/`            | Textus AI. `editor-plugins.ts` registers its plugins on the editor. `chat/` is the conversation the three chat surfaces share, with `quick-ask/`, `panel/` and `window/`; `revision/` turns a proposed edit into decided sentences; `margin/` draws revisions and notes beside the text; `writing/` is completion and the grammar check; `paper/` the long-form paper writer ([ai-paper.md](../design/ai-paper.md)); `skills/` the built-in skills and their translations; `citations.ts` sources as footnotes.                                        |
| `export/`        | **File › Export**: PDF and HTML from the rendered page, everything else through pandoc.                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `theme/`         | How documents look and how themes are read: the built-in themes, the document CSS (`content-base.css`), importing a Typora theme, the theme gallery, window colours from a theme (`engine/tokens/palette.ts`), putting the appearance settings on the page.                                                                                                                                                                                                                                                                                            |
| `settings/`      | The Settings window: `pages/` (General, Editor, AI, Extensions with the plugin pages, Create), `sections/` (the rows; `controls.tsx` holds `SettingRow` and `ToggleRow`), search over the pages (`settings-search.ts`).                                                                                                                                                                                                                                                                                                                                |
| `files/`         | The folder and its notes: the file tree, file search, the outline panel, moving files with their links, and the links between notes - one resolver for every way a link is followed (`link-resolve.ts`, its cases shared with the backend's `resolve_note_link`, `backend/links.go`), writing a link the way the folder does.                                                                                                                                                                                                                          |
| `clipboard/`     | What a copy tells plugins (`copied.ts`, `copied-plugin.ts`) and Copy Path. The clipboard history itself is the official Clipboard History plugin.                                                                                                                                                                                                                                                                                                                                                                                                      |
| `mobile/`        | Phone-only screens: the document list and menu, the outline sheet, the sidebar pushed in, whether the keyboard is up; `styles/` holds the phone layout's CSS ([ios-code-map.md](ios-code-map.md)).                                                                                                                                                                                                                                                                                                                                                     |
| `onboarding/`    | The first-run language picker and the tutorial.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `plugins/`       | The plugin system ([below](#the-plugin-system)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `engine/`        | What every surface is drawn and moved with, knowing no document, AI or plugin, each part using only those before it: `tokens/` (sizes, the interface scale, the one palette a theme's base colours derive into, `palette.ts`), `motion/` (timing, FLIP, presence), `drag/` (a drag, a row making room, the floating copy), `controls/` (dialogs, `Select`, `ContextMenu`, `Tooltip`, `InfoTip`, `PanelHeader`, `ResizeHandle`, `Sheet`, `CornerToast`, notices, the quick pick, icons), `layout/` (the island control).                                |
| `prefs/`         | The settings model: the `Settings` type and defaults (`settings-types.ts`), reading a stored file back (`settings-normalise.ts`), the store and its sync between windows, `SettingsProvider` / `useSettings` (`SettingsContext.tsx`), the rebindable shortcuts (`shortcut-defaults.ts`).                                                                                                                                                                                                                                                               |
| `storage/`       | Persisted state through the backend: the boot snapshot, pending writes, UI state, persisted lists, a project's `.textus/settings.json` (`project-settings.ts`), the webview's own localStorage (`local.ts`).                                                                                                                                                                                                                                                                                                                                           |
| `platform/`      | The backend bridge ([below](#talking-to-the-backend)), OS and capability facts (`os.ts`, `capabilities.ts`), window chrome, events, iOS file access, menu events on iOS.                                                                                                                                                                                                                                                                                                                                                                               |
| `i18n/`          | The UI strings (`locales/`), language names, plural rules. See [translating.md](translating.md).                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `utils/`         | Pure helpers (paths, CJK, word count, shortcuts, events, front matter) and small React hooks (`hooks/`).                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `lazy/`          | Loading on first use: `lazyModule`, `lazyComponent`, and warming what is likely soon.                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `help/`          | The bundled Markdown and AI guides, one file per language.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `playground/`    | The browser playground: the editor alone in a web page.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `dev/`           | The browser shim that stands in for the backend under `npm run dev` and in the e2e flows (`host-shim.ts`, with `plugin-shim-api.ts` for the plugin commands).                                                                                                                                                                                                                                                                                                                                                                                          |

Tests sit next to the file they test (`x.ts` and `x.test.ts`). End-to-end
flows are in `e2e/`.

## The plugin system

`src/plugins/` runs plugins in sandboxed frames and registers what they
add into the app's own registries (`registry/commands.ts`,
`app/frame/sidebar-tabs.tsx`, `engine/layout/store.ts`, the editor's extension
points). Its folders, by job:

| `plugins/`       | Holds                                                                                                                                                                                                                                      |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `host/`          | Running a plugin: the sandboxed frames, the runtimes put in them (`runtime/host-*.js` joined by `host-runtime.ts`, `text*.js`, `webview-*.js` joined by `webview-runtime.ts`), the broker that answers every call (`broker*.ts`), the RPC. |
| `contributions/` | The registries of what plugins add: commands and menus, code blocks, decorations, completions, hover cards, links, lists, AI tools, before-save.                                                                                           |
| `views/`         | What draws plugins: webviews, lists, web pages, forms, floating views, the theme given to a page.                                                                                                                                          |
| `store/`         | What is installed and how: manifests, the catalog, records and their state, permissions, settings, logs, export, migration, offers.                                                                                                        |
| `workspace/`     | What plugins know of the folder: the metadata index, globs.                                                                                                                                                                                |
| `background/`    | Scheduled jobs and the hidden background host window that runs them when no editor window is open (desktop).                                                                                                                               |

At its top: `manager.ts` (records, install, on and off; the loader),
`app-bridge.ts` (what the window lends plugins, and `emitPluginEvent` for
what they hear), `api-permissions.ts` (the one table of which call needs
which permission, read by the broker and the install check),
`panels-state.ts` and `ai-state.ts` (what plugins hear of the window's
panels and of Textus AI) and `plugin-uri.ts`. The window's side of the
bridge is `app/usePluginWindow.ts`; a plugin Textus AI writes in a reply
is read in `ai/chat/reply-plugin.ts`.

The public API is `docs/dev/textus.d.ts`; [plugins/](plugins/README.md) is
the plugin author's guide, and [recipes.md](recipes.md#add-a-plugin-api-call)
says how a call is added.

## Talking to the backend

The frontend reaches the Go backend only through `src/platform/`:

- **`ipc.ts`** exports one object per area (`fs`, `drafts`, `session`,
  `ai`, `exportDoc`, …); the areas are split over `ipc-app.ts`,
  `ipc-files.ts`, `ipc-plugins.ts`, `ipc-auth.ts`, `ipc-schedule.ts` and
  `ipc-ai-types.ts`, and `ipc.ts` re-exports them.
- **`ipc-call.ts`** is how every command is called: `command(name, …args)`
  for a command with typed bindings, `callBytes(name, args)` for one
  answering bytes (a Go `[]byte`, sent as base64);
  `aiCommand` refuses while Textus AI is off; `desktopQueryCommand`
  answers a fallback and `desktopActionCommand` rejects on iOS, where
  desktop-only commands aren't registered.
- **`bindings/*.ts`** name every command and its types. They were generated
  from the Rust commands up to 1.4.7 and are kept by hand since: `go test
./backend/` and `src/platform/commands.test.ts` fail while a command the
  frontend calls has no handler.
- **`native/`** is the bridge to MyGo: `core.ts` runs a command through the
  one Go service, `Host` (`Host.Invoke`, or `Host.Stream` for a command
  given a `Channel`), and rejects with the backend's error text;
  `event.ts` hands out the events the backend sends, all in one MyGo event
  (`textus:event`, `{event, payload}`); `window.ts` is this window (close,
  minimize, full screen, its close request); `host-client.ts` is written by
  `mygo generate` (`npm run bindings`) from `backend/host.go`. Outside a
  MyGo window, `window.__TEXTUS_HOST__` stands in for the backend: the dev
  shim (`src/dev/host-shim.ts`), the playground's, a test's.
- **`events.ts`** listens to events sent to this window, and sends events
  to other windows.

[recipes.md](recipes.md#add-a-backend-command) walks through a new command
end to end.

## CSS

- Component CSS lives next to its component and is imported by it. It
  reads tokens only - colours (`--ui-*`, `--doc-*`), shapes (`--shape-*`),
  text roles (`--text-*`), control heights, spacing and motion - by the
  rules in [design-language.md](../design/design-language.md); a raw
  colour, radius or font size is a review comment.
- The main window's own layout is `app/App.css`, a list of `@import`s in
  cascade order: the base tokens, then the window bar, sidebar, tabs, text
  column, notices, dialogs, sidebar panels, print rules and modes. Later
  files win ties, so keep print rules after what they hide and the modes
  last. Each piece lives in the folder of the feature it styles.
- The document's look is `theme/content-base.css` (a cascade layer, so an
  imported Typora theme or the user's Custom CSS wins over it) and
  `editor/milkdown-theme.css` (what makes the editor work: carets, markers,
  previews - not in the layer, so a theme can't break them).
- An imported theme is CSS text: `theme-import.ts` inlines its `@import`s
  and assets, `typora-compat.ts` adapts Typora selectors,
  `legacy-variables.ts` reads pre-1.3 variable names under today's, and
  `engine/tokens/palette.ts` reads its colours to tint the window around it.

## The backend

`main.go` starts the app (`backend.Run`); `mygo.json` is MyGo's
configuration (name, identifier, version, icons, file associations, the
frontend's dev server and build). The backend is the Go package
`backend/`, with the packages `backend/ai` and `backend/extract` beside it:

| Path                                                                                     | Holds                                                                                                                                             |
| ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `app.go`, `host.go`, `errors.go`                                                         | App setup and the `Host` service: the command registry (`command(name, fn)`), streams, and errors as `"<English> [code]"` (src/i18n/errors.json). |
| `events.go`, `windows.go`, `session.go`                                                  | Events to windows (`emitTo`), editor windows (built, numbered, reopened, closed with the page's say), and the window session.                     |
| `boot.go`, `boot.js`, `settings.go`, `store.go`, `stores.go`                             | The boot script every window gets, the settings, UI state and other files ([storage.md](storage.md)).                                             |
| `drafts.go`, `docdata.go`, `chathistory.go`                                              | Drafts, a document's data beside it, the chat history folder.                                                                                     |
| `files.go`, `pictures.go`, `tree.go`, `folder.go`, `walk.go`, `atomic.go`, `hidden_*.go` | Files and folders: reading, atomic writes, pictures beside a document, the sidebar's tree and its operations, the folder walk and watching.       |
| `conventions.go`, `jsonc.go`, `links.go`                                                 | What a folder says about how it is written (`folder_conventions`), and links between documents.                                                   |
| `background.go`, `tray.go`, `icons/tray/`                                                | Background mode: whether closing the last window quits, and its tray icon and menu ([lifecycle.md](lifecycle.md#background-mode)).                |
| `menu.go`, `menu_bar.go`, `menu_dispatch.go`, `menu_recents.go`                          | The menu table (src/registry/menu/menu.json), the macOS bar, the id → `menu-<id>` dispatch, Open Recent.                                          |
| `dialogs.go`, `shell.go`, `system.go`, `web.go`                                          | File dialogs, opening and revealing, system facts, clipboard, the window commands, and what is fetched from the web for the page.                 |
| `export.go`, `themes.go`, `schemes.go`                                                   | Export through pandoc, imported themes, and the `asset:` / `font:` schemes that serve files to the page.                                          |
| `export_pdf.go`, `pdfread.go`, `pdffile.go`, `pdfnumbers.go`                             | PDF export: the page's document printed by the webview (MyGo's PrintToPDF), with page numbers stamped onto the file.                              |
| `updater.go`, `net.go`                                                                   | The update check and install (`textus-latest.json`, never `latest.json`), and the proxy the app's requests go through.                            |
| `keys.go`, `imagehost.go`                                                                | The services' keys (`provider_keys.json`, owner-only), and pasted pictures uploaded to an image host.                                             |
| `themegallery.go`, `extstats.go`, `word.go`                                              | The theme gallery, the extensions' download counts, and Word (.docx) import.                                                                      |
| `ai_endpoints.go`, `ai_state.go`, `ai_suggest.go`                                        | Textus AI: custom endpoints, model lists, kept proposals, revision choices, completions, titles, next messages.                                   |
| `ai/`                                                                                    | Package `ai`: the provider catalog, how each server takes a key, routes, one request in each dialect, completions.                                |
| `extract/`                                                                               | Package `extract`: PDF, Word, PowerPoint, spreadsheets, EPUB, RTF and HTML as Markdown, for Word import, the AI and `textus read`.                |
| `cli_*.go`, `cli/`                                                                       | The `textus` terminal command: run before the app (`RunCLI`), answered by the running app, and installed ([cli.md](cli.md)).                      |
| `stubs.go`                                                                               | The iOS build's own commands, which fail on the desktop: no desktop command waits there any more.                                                 |

Not in the Go backend yet (the Rust sources are at
`git show 8da2d2f0:src-tauri/...`): the platform
touches `system.go` answers without acting on them (the Roman input
source for a plugin's key mode, the Edit menu's native actions, drag
modifiers), the AI's live evals ([ai-eval.md](../design/ai-eval.md)),
the chat's `edit_chapters` tool (a helper per chapter for a change through
a long document), the portable Windows build (`distribution.go`), and the
iOS and Android builds. Web page tabs are native pages on macOS and
Windows only, and tabs drag between windows there
only: Linux, Wayland above all, gives no window positions.
`src/platform/capabilities.ts` turns the frontend's side of what is
missing off.

A shape that exists on both sides carries a comment on each side naming
the other; change both together.

## Where things meet

`document-view/MilkdownEditor.tsx` (one tab's editor and every AI surface
attached to it) and `app/App.tsx` (the window) are where everything meets,
and are kept as compositions: each calls hooks and renders components that
live beside it (`document-view/use*.ts`, `app/useWindow*.ts`,
`app/frame/Sidebar.tsx`, `app/islands/DocumentIslands.tsx`) or in the feature's own folder
(`ai/margin/useReviewNotes.ts`, `editor/modes/...`). When you add to them,
add a hook or a component the same way - as `WindowBar`,
`useSidePanels` or `useUpdateChecks` are - rather than more code in
the file itself, and keep the hooks' call order.

## The window's Content Security Policy

Every window's page loads under the policy in `src/platform/window-csp.json`.
It keeps the network out of everything a stylesheet or a document can make
the page load - an imported or gallery theme, Custom CSS, a document's
HTML - while the app's own traffic goes through the backend, so the page
itself never needs a remote origin. MyGo sends no policy of its own: the
built pages carry it in a `<meta>` (`windowCspMeta` in `vite.csp.ts`), and
the dev server sends it as a header, which `mygo dev` and the e2e flows
load. `src/plugins/host/window-csp.test.ts` checks the rules below.

| Directive     | Allows                                                                                  | Because                                                                                                                                              |
| ------------- | --------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `default-src` | `'self'`                                                                                | Anything not listed: the app's own files only.                                                                                                       |
| `script-src`  | `'self'`, `'unsafe-inline'`, `'wasm-unsafe-eval'`, `data:`, `blob:`, the plugins' files | A plugin's page is a `srcdoc` frame, which takes the window's policy on top of its own, so the window allows what `webviewCsp` (`frames.ts`) allows. |
| `style-src`   | `'self'`, `'unsafe-inline'`, `data:`, the plugins' files                                | Themes and Custom CSS are `<style>` elements, and the editor sets `style=""`. No remote `@import`.                                                   |
| `img-src`     | `'self'`, `data:`, `blob:`, the asset protocol, the plugins' files, `https:`, `http:`   | A document shows its pictures from the web as written. A stylesheet's `url()` picture is one too; CSP can't tell the two apart.                      |
| `font-src`    | `'self'`, `data:`, the asset protocol, the plugins' files                               | KaTeX's fonts are bundled; an imported theme's fonts are inlined as `data:` (`theme-import.ts`). No web fonts.                                       |
| `media-src`   | `'self'`, `data:`, `blob:`, the asset protocol, the plugins' files                      | What a plugin's page may play.                                                                                                                       |
| `connect-src` | `'self'`, `data:`, `blob:`, the asset protocol, the plugins' files                      | Reading a picture's bytes back for export and copy. No `fetch` to the web (MyGo's calls go by message, not by request).                              |
| `worker-src`  | `'self'`, `data:`, `blob:`, the plugins' files                                          | A plugin's page may start a worker from its own files.                                                                                               |
| `frame-src`   | `'self'`, `https:`                                                                      | The framed web page on Linux and phones, and a plugin page's frames of the hosts the user granted (`webviewCsp` narrows them per plugin).            |
| `object-src`  | `'none'`                                                                                | No plugins of the old kind.                                                                                                                          |
| `base-uri`    | `'self'`                                                                                | A `<base>` can't move where relative URLs go.                                                                                                        |

Each scheme appears in both of its forms: `asset:` on macOS and Linux,
`http://asset.localhost` on Windows (WebView2), and the same for
`textus-plugin:`, which the plugins will use (not served by the Go backend
yet). The policy holds no
hashes or nonces: with one in a directive the browser ignores
`'unsafe-inline'`, and themes and plugin frames would stop working.
