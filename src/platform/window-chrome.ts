import {
  isAndroidPlatform,
  isIOSPlatform,
  isLinuxPlatform,
  isMacPlatform,
  isWindowsPlatform,
} from "./os";
import { hasAppMenu, isMobileApp } from "./capabilities";
import { isPhoneLayout, onPhoneLayout } from "./phone-layout";
import { readBoot } from "../storage/boot";
/**
 * How much of the window frame this app draws itself. macOS keeps its native
 * frame under an overlay title bar; Windows and Linux drop it, so the caption
 * buttons and menu entry are ours. Must agree with backend/windows.go.
 */

/**
 * Whether the OS frame is gone and the app owns the caption. Read once: it
 * cannot change for a window.
 */
export const appDrawsWindowFrame =
  hasAppMenu && (isWindowsPlatform() || isLinuxPlatform());

/**
 * Whether the frontend must handle the menu's accelerators: wherever the
 * app draws the frame, Windows and Linux, the windows have no native menu
 * bar to fire them (backend/menu.go installs one on macOS only).
 */
export const ownsMenuAccelerators = appDrawsWindowFrame;

/**
 * Whether the app draws inner resize edges. No: a MyGo window keeps the
 * system's resize edges with its title bar hidden.
 */
export const drawsWindowEdges = false;

/**
 * Whether the page draws the window's outline: Windows 10, whose undecorated
 * window is built without its shadow, the shadow's accent-coloured border
 * with it. The backend says which Windows in the boot snapshot, so the
 * first frame already has it.
 */
export function drawsWindowOutline(): boolean {
  return (
    appDrawsWindowFrame && isWindowsPlatform() && readBoot()?.windows10 === true
  );
}

/**
 * Which platform's webview this is, for `data-platform`. iOS is checked
 * first: an iPad reports "MacIntel" like a Mac.
 */
export function platformName():
  "macos" | "ios" | "android" | "windows" | "linux" {
  if (isIOSPlatform()) return "ios";
  if (isAndroidPlatform()) return "android";
  if (isMacPlatform()) return "macos";
  if (isWindowsPlatform()) return "windows";
  return "linux";
}

/**
 * Publishes `data-window-chrome` and `data-platform` on <html>, before the
 * first render so the title row never paints at the wrong inset: "mobile"
 * for the phone's layout, "app" for a tablet's (the desktop's, its menu and
 * frame drawn by the app), else the desktop's own. A tablet that turns to the
 * phone's width (split screen) changes with it.
 */
export function publishWindowChrome(): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  const publish = () => {
    root.dataset.windowChrome = isMobileApp
      ? isPhoneLayout()
        ? "mobile"
        : "app"
      : appDrawsWindowFrame
        ? "app"
        : "native";
  };
  publish();
  if (isMobileApp) onPhoneLayout(publish);
  root.dataset.platform = platformName();
}
