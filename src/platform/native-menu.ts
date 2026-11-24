import { invoke } from "./native/core";
import { inBrowser } from "./runtime";

type NativeMenuEntry = { label: string; run: () => void } | "separator";

/** A menu of the system's own at the pointer: for a window too small to
 *  draw one in. Nothing in the browser build. The backend shows it and
 *  answers which entry was picked (backend/menu.go, `popup_menu`). */
export async function popUpNativeMenu(entries: NativeMenuEntry[]) {
  if (inBrowser) return;
  const labels = entries.map((entry) =>
    entry === "separator" ? "" : entry.label,
  );
  const picked = await invoke<number | null>("popup_menu", { labels });
  const entry = picked === null ? undefined : entries[picked];
  if (entry && entry !== "separator") entry.run();
}
