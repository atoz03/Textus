// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DocTab } from "../document/doc-tabs";

const win = vi.hoisted(() => ({
  request: undefined as
    undefined | ((event: { preventDefault: () => void }) => Promise<void>),
  shown: 0,
  draftFails: false,
}));

vi.mock("../platform/this-window", () => ({
  thisWindow: () => ({
    show: () => {
      win.shown++;
      return Promise.resolve();
    },
    onCloseRequested: (
      handler: (event: { preventDefault: () => void }) => Promise<void>,
    ) => {
      win.request = handler;
      return Promise.resolve(() => {});
    },
  }),
}));

vi.mock("../platform/ipc", () => ({
  drafts: {
    saveDraftSnapshot: () =>
      win.draftFails
        ? Promise.reject(new Error("disk full"))
        : Promise.resolve(),
    clearDraftSnapshot: () => Promise.resolve(),
  },
  session: { cancelQuit: () => Promise.resolve() },
}));

vi.mock("../storage/pending-writes", () => ({
  flushPendingWrites: () => Promise.resolve(),
}));

import { useWindowCloseGuard, type PendingClose } from "./close-flow";

const tab = (content: string): DocTab =>
  ({
    id: "t1",
    path: "/notes/a.md",
    content,
    savedContent: "saved",
    diskMtime: null,
    sourceMode: false,
    reloadKey: 0,
  }) as DocTab;

/** The window asked to close, as a quit asks every window: whether the
 *  close was refused, and what is pending. */
async function askToClose(tabs: DocTab[], keepUnsaved: boolean) {
  const pending: (PendingClose | null)[] = [];
  renderHook(() =>
    useWindowCloseGuard(
      () => tabs,
      () => Promise.resolve(),
      (close) => pending.push(close),
      keepUnsaved,
    ),
  );
  await act(async () => {});
  let refused = false;
  await act(() => win.request!({ preventDefault: () => (refused = true) }));
  return { refused, pending };
}

beforeEach(() => {
  win.request = undefined;
  win.shown = 0;
  win.draftFails = false;
});

describe("a window asked to close", () => {
  it("comes forward with its question when a document is unsaved", async () => {
    const { refused, pending } = await askToClose([tab("changed")], false);
    expect(refused).toBe(true);
    expect(pending).toEqual([{ kind: "window" }]);
    expect(win.shown).toBe(1);
  });

  it("comes forward when a draft can't be kept", async () => {
    win.draftFails = true;
    const { refused } = await askToClose([tab("changed")], true);
    expect(refused).toBe(true);
    expect(win.shown).toBe(1);
  });

  it("closes where it is when nothing needs asking", async () => {
    const saved = await askToClose([tab("saved")], false);
    expect(saved.refused).toBe(false);
    const kept = await askToClose([tab("changed")], true);
    expect(kept.refused).toBe(false);
    expect(win.shown).toBe(0);
  });
});
