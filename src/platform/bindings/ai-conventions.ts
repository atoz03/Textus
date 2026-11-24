// The backend's commands and their types: the contract backend/ (Go, on
// MyGo) answers. Generated from the Tauri commands up to 1.4.7, kept by
// hand since; `go test ./backend/` fails while a command named here has
// no handler.


/* Types */
/**  The ten document languages the rules engine knows. */
export type Lang = "en" | "zh-Hans" | "zh-Hant" | "ja" | "ko" | "es" | "fr" | "de" | "ru" | "pt";

