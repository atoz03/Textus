import { isCallError, isMyGo, runtime } from "mygo-runtime";
import { Host } from "./host-client";

/**
 * The desktop backend's command contract: a command's name and its
 * arguments by name, answered with a value or rejected with the backend's
 * error string ("<English> [code]", src/i18n/errors.json). The bindings in
 * ../bindings are typed against it.
 *
 * In a MyGo window it goes through Host, the one service the Go side binds
 * (backend/host.go). Elsewhere `window.__TEXTUS_HOST__` stands in for the
 * backend: the dev shim (src/dev/host-shim.ts), the playground's stand-in,
 * a test's fake. With neither, every command rejects.
 */

/** A command's arguments, by name. */
export type InvokeArgs = Record<string, unknown>;

/** An event as its listeners get it: its name, the listener's id, and what
 *  it carries. */
export interface HostEventMessage<T = unknown> {
  event: string;
  id: number;
  payload: T;
}

/** What stands in for the backend outside a MyGo window. */
export interface HostStandIn {
  /** The window's label; "main" when not given. */
  label?: string;
  invoke(command: string, args?: InvokeArgs): Promise<unknown>;
  /** Subscribes to an event sent to this window; returns the unsubscribe. */
  listen?(
    event: string,
    handler: (event: HostEventMessage) => void,
  ): () => void;
  /** Sends an event to the window labelled `target`, "*" for every one. */
  emit?(target: string, event: string, payload: unknown): Promise<void>;
  /** A URL the page loads a file on disk from; "" where nothing serves it. */
  convertFileSrc?(path: string, protocol: string): string;
}

declare global {
  interface Window {
    __TEXTUS_HOST__?: HostStandIn;
  }
}

/** The stand-in for the backend, read per call: a test may install it late. */
export function standIn(): HostStandIn | undefined {
  return typeof window === "undefined" ? undefined : window.__TEXTUS_HOST__;
}

/** Whether a backend answers here: MyGo's, or a stand-in. */
export function hasHost(): boolean {
  return isMyGo() || standIn() !== undefined;
}

/**
 * Messages a command sends while it works (the bindings' `Channel`
 * arguments): set `onmessage`, then pass it among the command's arguments.
 * A command takes at most one.
 */
export class Channel<T = unknown> {
  onmessage: (message: T) => void;

  constructor(onmessage?: (message: T) => void) {
    this.onmessage = onmessage ?? (() => {});
  }

  /** Never sent as an argument's value: the call carries it apart. */
  toJSON(): null {
    return null;
  }
}

/** The backend's error as its callers compare it: the Go error's text. */
function unwrap(err: unknown): unknown {
  return isCallError(err) ? err.message : err;
}

async function invokeMyGo(
  command: string,
  args: InvokeArgs = {},
): Promise<unknown> {
  const channels = Object.keys(args).filter(
    (name) => args[name] instanceof Channel,
  );
  try {
    if (channels.length === 0) return await Host.invoke(command, args);
    if (channels.length > 1)
      throw new Error(`${command} takes one channel, not ${channels.length}`);
    const [name] = channels;
    const channel = args[name] as Channel;
    const rest = { ...args };
    delete rest[name];
    const stream = runtime().channel<unknown>((message) =>
      channel.onmessage(message),
    );
    return await Host.stream(command, rest, stream);
  } catch (err) {
    throw unwrap(err);
  }
}

/** Runs a backend command. */
export async function invoke<T>(
  command: string,
  args?: InvokeArgs,
): Promise<T> {
  if (isMyGo()) return (await invokeMyGo(command, args)) as T;
  const host = standIn();
  if (!host) throw new Error(`${command}: no desktop backend here`);
  return (await host.invoke(command, args)) as T;
}

/** Sends an event to the window labelled `target` ("*": every window). */
export async function emitTo(
  target: string,
  event: string,
  payload: unknown = null,
): Promise<void> {
  if (isMyGo()) {
    try {
      await Host.emit(target, event, payload);
    } catch (err) {
      throw unwrap(err);
    }
    return;
  }
  await standIn()?.emit?.(target, event, payload);
}

/**
 * A URL the page can load the file at `path` from, served by the backend's
 * scheme `protocol` (backend/schemes.go). WebView2 serves a custom scheme
 * at http://<scheme>.localhost, the other webviews at <scheme>://localhost.
 */
export function convertFileSrc(path: string, protocol = "asset"): string {
  if (!isMyGo()) return standIn()?.convertFileSrc?.(path, protocol) ?? "";
  const encoded = encodeURIComponent(path);
  return runtime().platform === "win32"
    ? `http://${protocol}.localhost/${encoded}`
    : `${protocol}://localhost/${encoded}`;
}
