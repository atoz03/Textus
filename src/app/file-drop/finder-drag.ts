import { listenToThisWindow, unlistenAll } from "../../platform/events";
import { windowIpc } from "../../platform/ipc";
import type { DraggedFile } from "../../engine/controls/document-drop";

type Point = { x: number; y: number };

/** What the window's drag events carry: the paths on entering (when the
 *  webview tells them) and on the drop, the pointer in CSS pixels
 *  throughout, and Shift when the webview tells it. */
interface DragPayload {
  paths?: string[];
  position: Point;
  shift?: boolean;
}

export interface SystemDrag {
  over(files: readonly DraggedFile[], at: Point, shift: boolean): void;
  leave(): void;
  drop(files: readonly DraggedFile[], at: Point, shift: boolean): void;
}

/**
 * Files dragged over the window from a file manager, through the window's
 * drag events (platform/native/event.ts): which of them are folders is
 * known soon after their paths. Shift is as the last drag event told it;
 * where none tells it, it is asked of the system (`drag_modifiers`), at
 * most once a frame, and again on the drop. Each answer is dropped if the
 * drag has moved on by then.
 */
export function watchSystemDrags(
  to: SystemDrag,
  isFolder: (path: string) => Promise<boolean>,
): () => void {
  let drag = 0;
  let files: Promise<DraggedFile[]> = Promise.resolve([]);
  let frame = 0;
  let at: Point = { x: 0, y: 0 };
  let held: boolean | undefined;
  const filesOf = (paths: string[]) =>
    Promise.all(
      paths.filter(Boolean).map(async (path) => ({
        path,
        isDir: await isFolder(path),
        fromSystem: true,
      })),
    );
  const shift = () =>
    held !== undefined
      ? Promise.resolve(held)
      : windowIpc.dragModifiers().then(
          (m) => !!m?.shift,
          () => false,
        );
  const told = (payload: DragPayload) => {
    if (typeof payload.shift === "boolean") held = payload.shift;
  };
  const show = () => {
    if (frame) return;
    const asked = drag;
    frame = requestAnimationFrame(() => {
      frame = 0;
      void Promise.all([files, shift()]).then(([now, down]) => {
        if (asked === drag) to.over(now, at, down);
      });
    });
  };
  const listeners = [
    listenToThisWindow<DragPayload>("native://drag-enter", (e) => {
      drag++;
      held = undefined;
      told(e.payload);
      files = filesOf(e.payload.paths ?? []);
      at = e.payload.position;
      show();
    }),
    listenToThisWindow<DragPayload>("native://drag-over", (e) => {
      told(e.payload);
      at = e.payload.position;
      show();
    }),
    listenToThisWindow<DragPayload>("native://drag-leave", () => {
      drag++;
      to.leave();
    }),
    listenToThisWindow<DragPayload>("native://drag-drop", (e) => {
      drag++;
      told(e.payload);
      const dropped = filesOf(e.payload.paths ?? []);
      const where = e.payload.position;
      void Promise.all([dropped, shift()]).then(([now, down]) =>
        to.drop(now, where, down),
      );
    }),
  ];
  return () => {
    cancelAnimationFrame(frame);
    unlistenAll(...listeners)();
  };
}
