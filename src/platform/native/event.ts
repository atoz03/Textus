import { isMyGo } from "mygo-runtime";
import { events } from "./host-client";
import { emitTo, standIn, type HostEventMessage } from "./core";

/**
 * Events from the backend to this window. The Go side sends each one to
 * the windows it is for, in one MyGo event ("textus:event", backend/
 * events.go) that carries its name and payload; listeners are kept here by
 * name. A page hears only what was sent to its own window, or to all.
 */

export type Event<T> = HostEventMessage<T>;
export type EventCallback<T> = (event: Event<T>) => void;
export type UnlistenFn = () => void;

export { emitTo };

const listeners = new Map<string, Map<number, EventCallback<unknown>>>();
let nextId = 1;
let subscribed = false;

function dispatch(event: string, payload: unknown): void {
  const named = listeners.get(event);
  if (!named) return;
  for (const [id, handler] of [...named])
    try {
      handler({ event, id, payload });
    } catch (err) {
      // One listener's failure is not the others'.
      setTimeout(() => {
        throw err;
      });
    }
}

function subscribe(): void {
  if (subscribed) return;
  subscribed = true;
  events.textusEvent.on(({ event, payload }) => dispatch(event, payload));
}

/**
 * Files dragged in from a file manager, as the page sees them: the
 * webview's own drag events give where the pointer is and the keys held
 * (Shift, Alt or Option, Ctrl), but no paths. These are told as
 * `native://drag-enter`, `-over` and `-leave`; the drop, with its paths,
 * comes from the backend (`native://drag-drop`).
 */
let watchingDrags = false;

function watchPageDrags(): void {
  if (watchingDrags || typeof window === "undefined") return;
  watchingDrags = true;
  let depth = 0;
  // A drag that started in this page is the page's own business.
  let ownDrag = false;
  window.addEventListener("dragstart", () => (ownDrag = true), true);
  window.addEventListener("dragend", () => (ownDrag = false), true);
  const fromSystem = (e: DragEvent) =>
    !ownDrag && !!e.dataTransfer?.types.includes("Files");
  const at = (e: DragEvent) => ({
    position: { x: e.clientX, y: e.clientY },
    shift: e.shiftKey,
    alt: e.altKey,
    ctrl: e.ctrlKey,
  });
  window.addEventListener(
    "dragenter",
    (e) => {
      if (!fromSystem(e)) return;
      // Entering a child comes before leaving its parent: only the window's
      // own edge brings the count to and from zero.
      if (depth++ === 0)
        dispatch("native://drag-enter", { paths: [], ...at(e) });
    },
    true,
  );
  window.addEventListener(
    "dragover",
    (e) => {
      if (fromSystem(e)) dispatch("native://drag-over", at(e));
    },
    true,
  );
  window.addEventListener(
    "dragleave",
    (e) => {
      if (!fromSystem(e) || depth === 0) return;
      if (--depth === 0) dispatch("native://drag-leave", {});
    },
    true,
  );
  window.addEventListener("drop", () => (depth = 0), true);
}

/** Listens to `event` sent to this window. */
export async function listen<T>(
  event: string,
  handler: EventCallback<T>,
): Promise<UnlistenFn> {
  if (!isMyGo()) {
    const host = standIn();
    return host?.listen
      ? host.listen(event, handler as (event: HostEventMessage) => void)
      : () => {};
  }
  subscribe();
  if (event.startsWith("native://drag-")) watchPageDrags();
  const id = nextId++;
  let named = listeners.get(event);
  if (!named) listeners.set(event, (named = new Map()));
  named.set(id, handler as EventCallback<unknown>);
  const mine = named;
  return () => {
    mine.delete(id);
    if (mine.size === 0 && listeners.get(event) === mine)
      listeners.delete(event);
  };
}
