// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { emitToWindow, listenToThisWindow, unlistenAll } from "./events";
import type { HostEventMessage } from "./native/core";

/** A stand-in backend (as the dev shim is) for the window `label`: what is
 *  sent to it, or to every window, reaches its listeners. */
function installFakeHost(label: string) {
  const listeners = new Map<string, ((e: HostEventMessage) => void)[]>();
  window.__TEXTUS_HOST__ = {
    label,
    invoke: () => Promise.reject(new Error("no commands here")),
    listen(event, handler) {
      listeners.set(event, [...(listeners.get(event) ?? []), handler]);
      return () =>
        listeners.set(
          event,
          (listeners.get(event) ?? []).filter((h) => h !== handler),
        );
    },
    emit(target, event, payload) {
      if (target === label || target === "*")
        for (const handler of listeners.get(event) ?? [])
          handler({ event, id: 0, payload });
      return Promise.resolve();
    },
  };
  return { listening: (event: string) => listeners.get(event)?.length ?? 0 };
}

afterEach(() => {
  delete window.__TEXTUS_HOST__;
});

describe("listenToThisWindow", () => {
  it("gets what is sent to this window, not another's, until unlistened", async () => {
    const host = installFakeHost("window-3");
    const got = vi.fn();
    const off = await listenToThisWindow<string>("open-paths-as-tabs", (e) =>
      got(e.payload),
    );
    await emitToWindow("window-3", "open-paths-as-tabs", "a.md");
    await emitToWindow("window-4", "open-paths-as-tabs", "b.md");
    expect(got.mock.calls).toEqual([["a.md"]]);
    off();
    expect(host.listening("open-paths-as-tabs")).toBe(0);
  });
});

describe("unlistenAll", () => {
  it("unsubscribes every listener once the cleanup runs", async () => {
    const first = vi.fn();
    const second = vi.fn();
    const cleanup = unlistenAll(
      Promise.resolve(first),
      Promise.resolve(second),
    );

    // Nothing happens at registration time - it only hands back a teardown.
    await Promise.resolve();
    expect(first).not.toHaveBeenCalled();

    cleanup();
    // The unlisten functions resolve asynchronously, so they run a tick later.
    await Promise.resolve();
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
  });
});
