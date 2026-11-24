import { invoke } from "./core";

/** Folders of the system, as the backend knows them (backend/system.go). */

/** The user's Documents folder. */
export function documentDir(): Promise<string> {
  return invoke<string>("document_dir");
}

/** The system's folder for temporary files. */
export function tempDir(): Promise<string> {
  return invoke<string>("temp_dir");
}

/** `parts` joined with the separator the first of them uses. */
export function join(...parts: string[]): Promise<string> {
  const sep = parts.some((p) => /^[A-Za-z]:\\/.test(p)) ? "\\" : "/";
  const joined = parts
    .filter(Boolean)
    .map((part, i) =>
      i === 0
        ? part.replace(/[\\/]+$/, "")
        : part.replace(/^[\\/]+|[\\/]+$/g, ""),
    )
    .join(sep);
  return Promise.resolve(joined || sep);
}
