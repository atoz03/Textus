import { listenToThisWindow, unlistenAll } from "../../platform/events";
import { windowIpc } from "../../platform/ipc";
import type { DragModifiers } from "../../platform/bindings/app";
import type { DraggedFile } from "../../engine/controls/document-drop";
import { folderName } from "../../utils/path";
import { copyKey, rowsOut } from "../../files/drag-out";

type Point = { x: number; y: number };

/** The keys held over the drag: Shift opens; the copy key (Option, Ctrl
 *  elsewhere) copies the tree's own rows brought back. */
export interface DragHeld {
  shift: boolean;
  copy: boolean;
}

const NONE: DragHeld = { shift: false, copy: false };

/** `rows` when the system's drag carries them (by name: it may name a
 *  folder by where a link leads); else null, as for a drag out whose end
 *  was never heard. */
function sameNames(
  rows: readonly DraggedFile[] | null,
  paths: readonly string[],
): readonly DraggedFile[] | null {
  const names = (all: readonly string[]) =>
    all.map(folderName).sort().join("\n");
  return rows &&
    names(rows.map((row) => row.path)) === names(paths.filter(Boolean))
    ? rows
    : null;
}

/** What the window's drag events carry: the paths on entering (when the
 *  webview tells them) and on the drop, the pointer in CSS pixels
 *  throughout, and the keys held when the webview tells them. */
interface DragPayload {
  paths?: string[];
  position: Point;
  shift?: boolean;
  alt?: boolean;
  ctrl?: boolean;
}

interface SystemDrag {
  over(files: readonly DraggedFile[], at: Point, held: DragHeld): void;
  leave(): void;
  drop(files: readonly DraggedFile[], at: Point, held: DragHeld): void;
}

/**
 * Files dragged over the window from a file manager, through the window's
 * drag events (platform/native/event.ts): which of them are folders is
 * known soon after their paths. The keys are as the last drag event told
 * them; where none tells them, they are asked of the system
 * (`drag_modifiers`), at most once a frame, and again on the drop. Each
 * answer is dropped if the drag has moved on by then.
 */
export function watchSystemDrags(
  to: SystemDrag,
  isFolder: (path: string) => Promise<boolean>,
): () => void {
  let drag = 0;
  let files: Promise<DraggedFile[]> = Promise.resolve([]);
  let frame = 0;
  let at: Point = { x: 0, y: 0 };
  let told: DragModifiers | undefined;
  // The tree's own rows, dragged out and back, are no file manager's:
  // told as the drag comes in, before its drop ends the drag out, and
  // taken as they went out.
  let own: readonly DraggedFile[] | null = null;
  const filesOf = (paths: string[]): Promise<DraggedFile[]> =>
    own
      ? Promise.resolve(own.map((row) => ({ ...row, fromSystem: false })))
      : Promise.all(
          paths.filter(Boolean).map(async (path) => ({
            path,
            isDir: await isFolder(path),
            fromSystem: true,
          })),
        );
  const keys = () =>
    (told
      ? Promise.resolve(told)
      : windowIpc.dragModifiers().then(
          (m) => m,
          () => null,
        )
    ).then((m): DragHeld =>
      m ? { shift: m.shift, copy: copyKey() === "Alt" ? m.alt : m.ctrl } : NONE,
    );
  const tell = (payload: DragPayload) => {
    if (typeof payload.shift === "boolean")
      told = {
        shift: payload.shift,
        alt: !!payload.alt,
        ctrl: !!payload.ctrl,
      };
  };
  const show = () => {
    if (frame) return;
    const asked = drag;
    frame = requestAnimationFrame(() => {
      frame = 0;
      void Promise.all([files, keys()]).then(([now, held]) => {
        if (asked === drag) to.over(now, at, held);
      });
    });
  };
  const listeners = [
    listenToThisWindow<DragPayload>("native://drag-enter", (e) => {
      drag++;
      own = sameNames(rowsOut(), e.payload.paths ?? []);
      told = undefined;
      tell(e.payload);
      files = filesOf(e.payload.paths ?? []);
      at = e.payload.position;
      show();
    }),
    listenToThisWindow<DragPayload>("native://drag-over", (e) => {
      tell(e.payload);
      at = e.payload.position;
      show();
    }),
    listenToThisWindow<DragPayload>("native://drag-leave", () => {
      drag++;
      to.leave();
    }),
    listenToThisWindow<DragPayload>("native://drag-drop", (e) => {
      drag++;
      tell(e.payload);
      const dropped = filesOf(e.payload.paths ?? []);
      const where = e.payload.position;
      void Promise.all([dropped, keys()]).then(([now, held]) =>
        to.drop(now, where, held),
      );
    }),
  ];
  return () => {
    cancelAnimationFrame(frame);
    unlistenAll(...listeners)();
  };
}
