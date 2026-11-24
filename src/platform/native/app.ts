import { invoke } from "./core";

/** The app's version, as the backend was built with it (package.json's). */
export function getVersion(): Promise<string> {
  return invoke<string>("app_version");
}
