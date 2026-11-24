// @vitest-environment jsdom

import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { Channel, convertFileSrc, emitTo, invoke } from "./core";
import { listen } from "./event";
import { getCurrentWindow } from "./window";

/** What a MyGo window gives the page as `window.mygo`, kept to what the
 *  bridge uses: calls answered by `answer`, channels, and events the test
 *  sends with `send`. */
const fake = {
  platform: "linux",
  calls: [] as { method: string; args: unknown[] }[],
  answer: null as unknown as (method: string, args: unknown[]) => unknown,
  handlers: new Map<string, ((payload: unknown) => void)[]>(),
  send(name: string, payload: unknown) {
    for (const handler of this.handlers.get(name) ?? []) handler(payload);
  },
};

/** A Go error as MyGo rejects with it. */
function callError(message: string): Error {
  const err = new Error(message);
  err.name = "CallError";
  return err;
}

beforeAll(() => {
  (globalThis as { mygo?: unknown }).mygo = {
    get platform() {
      return fake.platform;
    },
    callWith(method: string, args: unknown[]) {
      fake.calls.push({ method, args });
      try {
        return Promise.resolve(fake.answer(method, args));
      } catch (err) {
        return Promise.reject(err);
      }
    },
    channel: (onmessage: (message: unknown) => void) => ({ onmessage }),
    on(name: string, handler: (payload: unknown) => void) {
      fake.handlers.set(name, [...(fake.handlers.get(name) ?? []), handler]);
      return () =>
        fake.handlers.set(
          name,
          (fake.handlers.get(name) ?? []).filter((h) => h !== handler),
        );
    },
  };
});

/** What every call answers unless a test says otherwise. */
const NOTHING = () => null;
fake.answer = NOTHING;

afterEach(() => {
  fake.calls = [];
  fake.answer = NOTHING;
  fake.platform = "linux";
});

/** Lets the promise chains started by an event run. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("invoke", () => {
  it("runs a command through Host.Invoke, its arguments by name", async () => {
    fake.answer = () => ({ text: "# Hi" });
    await expect(invoke("read_text_file", { path: "/a.md" })).resolves.toEqual({
      text: "# Hi",
    });
    expect(fake.calls).toEqual([
      { method: "Host.Invoke", args: ["read_text_file", { path: "/a.md" }] },
    ]);
  });

  it("rejects with the backend's error text, as callers compare it", async () => {
    fake.answer = () => {
      throw callError("No such file [notFound]");
    };
    await expect(invoke("read_text_file", { path: "/gone.md" })).rejects.toBe(
      "No such file [notFound]",
    );
  });

  it("streams a command with a channel through Host.Stream", async () => {
    const got: unknown[] = [];
    const channel = new Channel<string>((message) => got.push(message));
    fake.answer = (_method, args) => {
      const stream = args[2] as { onmessage: (m: unknown) => void };
      stream.onmessage("one");
      stream.onmessage("two");
      return "done";
    };
    await expect(
      invoke("ai_complete", { prompt: "hi", onEvent: channel }),
    ).resolves.toBe("done");
    expect(fake.calls[0]!.method).toBe("Host.Stream");
    expect(fake.calls[0]!.args.slice(0, 2)).toEqual([
      "ai_complete",
      { prompt: "hi" },
    ]);
    expect(got).toEqual(["one", "two"]);
  });

  it("refuses a command given two channels", async () => {
    await expect(
      invoke("x", { a: new Channel(), b: new Channel() }),
    ).rejects.toThrow("takes one channel");
    expect(fake.calls).toEqual([]);
  });
});

describe("emitTo", () => {
  it("sends through Host.Emit, a missing payload as null", async () => {
    await emitTo("window-2", "menu-save");
    expect(fake.calls).toEqual([
      { method: "Host.Emit", args: ["window-2", "menu-save", null] },
    ]);
  });
});

describe("convertFileSrc", () => {
  it("names the backend's scheme as each webview serves it", () => {
    expect(convertFileSrc("/n/a b.png")).toBe(
      "asset://localhost/%2Fn%2Fa%20b.png",
    );
    fake.platform = "win32";
    expect(convertFileSrc("C:\\n\\a.png", "font")).toBe(
      "http://font.localhost/C%3A%5Cn%5Ca.png",
    );
  });
});

describe("listen", () => {
  it("hands each listener the events of its name from the one envelope", async () => {
    const saves = vi.fn();
    const other = vi.fn();
    const off = await listen<string>("menu-save", (e) => saves(e.payload));
    await listen("menu-open", other);
    fake.send("textus:event", { event: "menu-save", payload: "a.md" });
    expect(saves).toHaveBeenCalledWith("a.md");
    expect(other).not.toHaveBeenCalled();
    off();
    fake.send("textus:event", { event: "menu-save", payload: "b.md" });
    expect(saves).toHaveBeenCalledTimes(1);
  });
});

describe("a window's close request", () => {
  it("asks the page while it listens, and closes unless the page says no", async () => {
    let keep = true;
    const off = await getCurrentWindow().onCloseRequested((event) => {
      if (keep) event.preventDefault();
    });
    expect(fake.calls).toContainEqual({
      method: "Host.Invoke",
      args: ["window_close_listen", { on: true }],
    });
    const request = { event: "native://close-requested", payload: null };
    fake.send("textus:event", request);
    await settle();
    expect(fake.calls.map((c) => c.args[0])).not.toContain("window_destroy");
    keep = false;
    fake.send("textus:event", request);
    await settle();
    expect(fake.calls.map((c) => c.args[0])).toContain("window_destroy");
    off();
    expect(fake.calls).toContainEqual({
      method: "Host.Invoke",
      args: ["window_close_listen", { on: false }],
    });
  });
});
