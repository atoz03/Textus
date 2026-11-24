import { readdirSync, readFileSync } from "node:fs";
import {
  MENU_TABLE,
  isMenu,
  type TableItem,
  type TableNode,
} from "../registry/menu/menu-table";

// Test-only: the native menu's keys as menu.json gives them and the events
// the backend's menu dispatch (backend/menu_dispatch.go) sends, for tests
// that check the frontend against them.

export type Platform = "macos" | "windows" | "linux";

const MENU_GO = readdirSync("backend")
  .filter((file) => /^menu.*\.go$/.test(file) && !file.endsWith("_test.go"))
  .map((file) => readFileSync(`backend/${file}`, "utf8"))
  .join("\n")
  .replace(/\r\n/g, "\n");

const TABLE_PLATFORM: Record<Platform, string> = {
  macos: "mac",
  windows: "windows",
  linux: "linux",
};

function items(nodes: readonly TableNode[] = []): TableItem[] {
  return nodes.flatMap((node): TableItem[] =>
    typeof node !== "object" || "sep" in node || "section" in node
      ? []
      : isMenu(node)
        ? items(node.items)
        : [node],
  );
}

/** What this build can't do yet, left off the bar (menu.go's
 *  notYetInMenu). */
const NOT_YET = new Set(
  [
    ...(
      MENU_GO.match(/notYetInMenu = \[\]string\{([^}]*)\}/)?.[1] ?? ""
    ).matchAll(/"([^"]+)"/g),
  ].map((match) => match[1]!),
);

/** Whether the backend leaves menu item `id` off the bar for now. */
export const leftOffBar = (id: string) => NOT_YET.has(id);

const ALL = MENU_TABLE.bar
  .flatMap((menu) => items(menu.items))
  .filter((item) => !item.id || !leftOffBar(item.id));

/** The menu items with a fixed accelerator on `platform`'s native bar, by
 *  key: the app's own items, not the predefined (role) ones. */
export function nativeAccelerators(platform: Platform): Map<string, string> {
  const here = TABLE_PLATFORM[platform];
  const out = new Map<string, string>();
  for (const item of ALL) {
    if (!item.keys || item.keysOff?.includes(here)) continue;
    if (item.on && !item.on.includes(here)) continue;
    const id =
      item.id ?? (item.macOwn && here === "mac" ? item.predefined : undefined);
    if (id) out.set(item.keys, id);
  }
  return out;
}

/** Which of the predefined (role) Undo, Redo and Select All a Mac's menu
 *  builds: each takes its key before the page, running WebKit's own
 *  action. */
export function predefinedOnMac(): string[] {
  return ALL.filter(
    (item) =>
      ["undo", "redo", "select-all"].includes(item.predefined ?? "") &&
      !item.macOwn,
  ).map((item) => item.predefined!);
}

/** A Go string constant of the menu's: an id or a prefix. */
function constant(name: string): string | undefined {
  return MENU_GO.match(new RegExp(`^\\s*${name}\\s*=\\s*"([^"]+)"$`, "m"))?.[1];
}

/** Whether the dispatch's `case` condition `cond` takes `id`: `id == "x"`,
 *  `id == someID` or `strings.HasPrefix(id, somePrefix)`, joined by `||`. */
function takes(cond: string, id: string): boolean {
  return cond.split("||").some((part) => {
    const literal = part.match(/id == "([^"]+)"/)?.[1];
    if (literal !== undefined) return literal === id;
    const named = part.match(/id == (\w+)/)?.[1];
    if (named) return constant(named) === id;
    const prefix = part.match(/strings\.HasPrefix\(id, (\w+)\)/)?.[1];
    return !!prefix && id.startsWith(constant(prefix) ?? "\0");
  });
}

/** The window event each menu id is sent as, from the backend's dispatch;
 *  null when the backend acts itself. */
export function eventOf(id: string): string | null {
  // The prefixes whose rest goes with the event.
  for (const [, name, event] of MENU_GO.matchAll(
    /\{(\w+), "(menu-[a-z-]+)"\}/g,
  )) {
    const prefix = constant(name!);
    if (prefix && id.startsWith(prefix)) return event!;
  }
  const dispatch = MENU_GO.slice(MENU_GO.indexOf("func (a *App) dispatchMenu"));
  const cases = dispatch
    .slice(0, dispatch.indexOf("\n\tdefault:"))
    .split("\n\tcase ")
    .slice(1);
  for (const branch of cases) {
    const cond = branch.slice(0, branch.indexOf(":\n"));
    if (!takes(cond, id)) continue;
    if (/"menu-"\s*\+\s*id|emitOrOpenWindow\(/.test(branch))
      return `menu-${id}`;
    return branch.match(/"(menu-[a-z-]+)"/)?.[1] ?? null;
  }
  const own = ALL.some((item) => item.id === id || item.predefined === id);
  return own ? `menu-${id}` : null;
}
