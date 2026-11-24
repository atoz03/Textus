import { getVersion } from "./native/app";
import { inBrowser } from "./runtime";

/** A newer version of the app, ready to install. */
export interface Update {
  /** The new version. */
  version: string;
  /** The update's handle, for the backend to install it after a quit. */
  rid: number;
  /** Downloads and installs it; the running app keeps running. */
  downloadAndInstall: () => Promise<void>;
}

/** The app's version; empty in the browser. */
export function appVersion(): Promise<string> {
  return inBrowser ? Promise.resolve("") : getVersion();
}
