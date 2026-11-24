import {
  emitTo,
  listen,
  type EventCallback,
  type UnlistenFn,
} from "./native/event";
import { inBrowser } from "./runtime";

/**
 * Listen to an event addressed to this window. The backend sends each
 * event only to the windows it is for, so another window's never fires
 * here.
 */
export function listenToThisWindow<T>(
  event: string,
  handler: EventCallback<T>,
): Promise<UnlistenFn> {
  if (inBrowser) return Promise.resolve(() => {});
  return listen<T>(event, handler);
}

/** Sends `event` to the window labelled `label`. */
export function emitToWindow(
  label: string,
  event: string,
  payload?: unknown,
): Promise<void> {
  return inBrowser ? Promise.resolve() : emitTo(label, event, payload);
}

/** Teardown for a batch of subscriptions, each of which resolves its
 *  unlisten function later: `return unlistenAll(...)` from the effect. */
export function unlistenAll(...subs: Promise<UnlistenFn>[]): () => void {
  return () => {
    for (const sub of subs) void sub.then((f) => f());
  };
}
