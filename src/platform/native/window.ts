import { isMyGo } from "mygo-runtime";
import { invoke, standIn } from "./core";
import {
  listen,
  type Event,
  type EventCallback,
  type UnlistenFn,
} from "./event";

/**
 * The window the page runs in, through the backend's window commands
 * (backend/system.go) and the window's own events (backend/windows.go).
 * Sizes and places are in physical pixels, as the callers had them from
 * Tauri: the backend's device-independent ones times the page's scale.
 */

export class PhysicalSize {
  readonly type = "Physical";
  constructor(
    public width: number,
    public height: number,
  ) {}
}

export class PhysicalPosition {
  readonly type = "Physical";
  constructor(
    public x: number,
    public y: number,
  ) {}
}

/** A request to close the window: unless a listener refuses it, the
 *  window is destroyed once the listeners have run. */
class CloseRequestedEvent {
  readonly event = "native://close-requested";
  private prevented = false;
  constructor(readonly id: number) {}
  preventDefault(): void {
    this.prevented = true;
  }
  isPreventDefault(): boolean {
    return this.prevented;
  }
}

type ResizeDirection =
  | "North"
  | "South"
  | "East"
  | "West"
  | "NorthEast"
  | "NorthWest"
  | "SouthEast"
  | "SouthWest";

export interface NativeWindow {
  readonly label: string;
  /** Asks to close: the close-requested listeners may refuse. */
  close(): Promise<void>;
  /** Closes without asking. */
  destroy(): Promise<void>;
  minimize(): Promise<void>;
  toggleMaximize(): Promise<void>;
  isMaximized(): Promise<boolean>;
  isFullscreen(): Promise<boolean>;
  setFullscreen(on: boolean): Promise<void>;
  setAlwaysOnTop(on: boolean): Promise<void>;
  /** The window system resizes the window's edges itself: nothing to do. */
  startResizeDragging(direction: ResizeDirection): Promise<void>;
  /** A window is moved by its `--app-region: drag` areas: nothing to do. */
  startDragging(): Promise<void>;
  outerPosition(): Promise<PhysicalPosition>;
  outerSize(): Promise<PhysicalSize>;
  scaleFactor(): Promise<number>;
  onCloseRequested(
    handler: (event: CloseRequestedEvent) => void | Promise<void>,
  ): Promise<UnlistenFn>;
  onFocusChanged(handler: EventCallback<boolean>): Promise<UnlistenFn>;
  onResized(handler: EventCallback<PhysicalSize>): Promise<UnlistenFn>;
  onMoved(handler: EventCallback<PhysicalPosition>): Promise<UnlistenFn>;
  listen<T>(event: string, handler: EventCallback<T>): Promise<UnlistenFn>;
}

interface Bounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

function currentLabel(): string {
  const boot = (window as { __BOOT__?: { label?: unknown } }).__BOOT__;
  const booted =
    typeof boot?.label === "string" && boot.label ? boot.label : null;
  if (isMyGo()) return booted ?? "main";
  return standIn()?.label ?? booted ?? "main";
}

const scale = () => window.devicePixelRatio || 1;

/** The window's place and size; the page's own view of them where the
 *  backend has none (a stand-in). */
async function bounds(): Promise<Bounds> {
  const told = await invoke<Bounds | null>("window_bounds").catch(() => null);
  return (
    told ?? {
      x: window.screenX,
      y: window.screenY,
      width: window.outerWidth,
      height: window.outerHeight,
    }
  );
}

const run = (command: string, args?: Record<string, unknown>) =>
  invoke<unknown>(command, args).then(() => undefined);

/** How many close-requested listeners this page has: while any, the
 *  backend asks the page before the window closes. */
let closeListeners = 0;

function nativeWindow(label: string): NativeWindow {
  const win: NativeWindow = {
    label,
    close: () => run("window_close"),
    destroy: () => run("window_destroy"),
    minimize: () => run("window_minimize"),
    toggleMaximize: () => run("window_toggle_maximize"),
    isMaximized: () => invoke<boolean>("window_is_maximized").then(Boolean),
    isFullscreen: () => invoke<boolean>("window_is_fullscreen").then(Boolean),
    setFullscreen: (on) => run("window_set_fullscreen", { on }),
    setAlwaysOnTop: (on) => run("window_set_always_on_top", { on }),
    startResizeDragging: () => Promise.resolve(),
    startDragging: () => Promise.resolve(),
    outerPosition: async () => {
      const { x, y } = await bounds();
      return new PhysicalPosition(
        Math.round(x * scale()),
        Math.round(y * scale()),
      );
    },
    outerSize: async () => {
      const { width, height } = await bounds();
      return new PhysicalSize(
        Math.round(width * scale()),
        Math.round(height * scale()),
      );
    },
    scaleFactor: () => Promise.resolve(scale()),
    onCloseRequested: async (handler) => {
      const off = await listen<null>("native://close-requested", (event) => {
        void (async () => {
          const request = new CloseRequestedEvent(event.id);
          await handler(request);
          if (!request.isPreventDefault()) await win.destroy();
        })();
      });
      if (closeListeners++ === 0)
        await run("window_close_listen", { on: true }).catch(() => {});
      let listening = true;
      return () => {
        if (!listening) return;
        listening = false;
        off();
        if (--closeListeners === 0)
          void run("window_close_listen", { on: false }).catch(() => {});
      };
    },
    onFocusChanged: async (handler) => {
      const offs = await Promise.all([
        listen<boolean>("native://focus", (e) =>
          handler({ ...e, payload: true }),
        ),
        listen<boolean>("native://blur", (e) =>
          handler({ ...e, payload: false }),
        ),
      ]);
      return () => offs.forEach((off) => off());
    },
    onResized: (handler) =>
      listen<{ width: number; height: number }>("native://resized", (e) =>
        handler({
          ...e,
          payload: new PhysicalSize(
            Math.round(e.payload.width * scale()),
            Math.round(e.payload.height * scale()),
          ),
        } satisfies Event<PhysicalSize>),
      ),
    onMoved: (handler) =>
      listen<{ x: number; y: number }>("native://moved", (e) =>
        handler({
          ...e,
          payload: new PhysicalPosition(
            Math.round(e.payload.x * scale()),
            Math.round(e.payload.y * scale()),
          ),
        } satisfies Event<PhysicalPosition>),
      ),
    listen: (event, handler) => listen(event, handler),
  };
  return win;
}

let current: NativeWindow | null = null;

/** The window this page runs in. */
export function getCurrentWindow(): NativeWindow {
  const label = currentLabel();
  if (current?.label !== label) current = nativeWindow(label);
  return current;
}
