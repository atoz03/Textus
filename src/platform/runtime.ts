import { hasHost } from "./native/core";

/** True in the browser build, where there is no desktop backend and the
 *  platform modules use browser stand-ins. Set at build time. */
export const inBrowser = import.meta.env.TEXTUS_BROWSER === true;

/** Whether a desktop backend answers here: the app's (MyGo), or the dev
 *  shim standing in for it. Read per call: the shim installs itself late. */
export function hasBackend(): boolean {
  return !inBrowser && hasHost();
}
