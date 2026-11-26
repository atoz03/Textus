#!/usr/bin/env node
// What a change needs in CI's quick suite, printed for $GITHUB_OUTPUT:
// `light` (GitHub's settings, the license or root Markdown alone: only their
// format and the READMEs' structure), `docs` (documentation alone: format,
// links and the tests that read the docs, no e2e), `native` (the Go checks
// on Linux), `roundtrip`, and `full` for a pull request that bumps the
// version, which gets the whole suite. A push runs native for everything but
// a bump or a light change; without a usable base everything runs.
import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, posix, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ownRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

/** Documentation: Markdown, docs/, the issue templates and the license. */
const DOCS = /(\.md$|^docs\/|^\.github\/ISSUE_TEMPLATE\/|^LICENSE$)/;

/** Markdown in these trees is the app's own content or test input, not
 *  documentation. */
const CODE_TREES = /^(src|e2e|backend)\//;

/** Changes that run everything: CI itself, the Node version, and the npm
 *  manifest and lockfile beyond a bump's version lines. */
const EVERYTHING =
  /^(\.github\/workflows\/|\.nvmrc$|package\.json$|package-lock\.json$)/;

/** What the native jobs build and test: the Go backend, its module and
 *  MyGo's configuration with what the app ships beside it, and the native
 *  check and smoke scripts. */
const NATIVE =
  /(^backend\/|^[^/]+\.go$|^go\.(mod|sum)$|^mygo\.json$|^resources\/|^packaging\/(windows|icons)\/|^scripts\/check-native\.mjs$|^scripts\/smoke-linux\.sh$)/;

/** Files no build, test or check reads beyond prettier and the README
 *  check: GitHub's settings and templates (not its workflows or actions),
 *  the license, the Markdown at the repository's root, and the READMEs' and
 *  store listings' pictures. */
const LIGHT =
  /^(\.github\/(?!workflows\/|actions\/)|LICENSE$|[^/]+\.md$|docs\/images\/)/;

/** What can change the save round trip's result, including the documents
 *  it reads. */
const ROUNDTRIP =
  /^(src\/editor\/|src\/document[^/]*\/|src\/utils\/markdown|src\/help\/|docs\/|package\.json$|package-lock\.json$|\.github\/workflows\/)/;

/** Whether the Go code reads `file`: one of `goReads` (goReads()), or in a
 *  folder ("x/") or under a name's start ("x/guide.") among them. */
export function readByGo(file, goReads = []) {
  return goReads.some(
    (read) =>
      read === file ||
      ((read.endsWith("/") || read.endsWith(".")) && file.startsWith(read)),
  );
}

/** Whether every file is documentation Markdown: no code, nothing Go
 *  reads, and not light (which needs even less). */
export function docsOnly({ files, goReads = [] }) {
  return (
    files.length > 0 &&
    files.every(
      (file) =>
        file.endsWith(".md") &&
        !CODE_TREES.test(file) &&
        !readByGo(file, goReads),
    ) &&
    !files.every((file) => LIGHT.test(file))
  );
}

/** Whether the change moves the app's version: package.json's `version`. */
export function bumpsVersion(before, after) {
  try {
    const old = JSON.parse(before ?? "null")?.version;
    const now = JSON.parse(after ?? "null")?.version;
    return Boolean(old && now && old !== now);
  } catch {
    return false;
  }
}

/** Whether `files` can change what the save round trip finds. */
export function reachesSaving({ files, push }) {
  return push || files.some((file) => ROUNDTRIP.test(file));
}

/** A JSON file whose only change may be the version fields `paths` name. */
function jsonVersions(...paths) {
  const get = (obj, path) => path.reduce((o, key) => o?.[key], obj);
  return (before, after) => {
    const old = JSON.parse(before);
    const now = JSON.parse(after);
    for (const path of paths) {
      const parent = get(old, path.slice(0, -1));
      if (parent === undefined) return false;
      parent[path.at(-1)] = get(now, path);
    }
    return JSON.stringify(old) === JSON.stringify(now);
  };
}

/** The files a version bump touches, each with the test that its change is
 *  the version alone. */
const VERSION_FILES = {
  "package.json": jsonVersions(["version"]),
  "package-lock.json": jsonVersions(["version"], ["packages", "", "version"]),
  "mygo.json": jsonVersions(["version"]),
};

function versionOnly(file, before, after) {
  if (before == null || after == null || !(file in VERSION_FILES)) {
    return false;
  }
  try {
    return VERSION_FILES[file](before, after);
  } catch {
    return false; // unparsable JSON: not a bump
  }
}

/** `read(file)` gives a file's text `before` and `after` the change (null
 *  where missing); `goReads` are what the Go code reads outside backend/. */
export function classify({ files, push, read, goReads = [] }) {
  if (
    files.length > 0 &&
    files.every((file) => LIGHT.test(file) && !readByGo(file, goReads))
  )
    return { light: true, native: false };
  const none = { light: false, native: false };
  const code = files.filter(
    (file) =>
      !DOCS.test(file) || CODE_TREES.test(file) || readByGo(file, goReads),
  );
  if (code.length === 0) return { ...none, native: push };
  const rest = code.filter((file) => !versionOnly(file, ...read(file)));
  if (rest.length === 0) return none;
  if (push || rest.some((file) => EVERYTHING.test(file)))
    return { ...none, native: true };
  return {
    ...none,
    native: rest.some((file) => NATIVE.test(file) || readByGo(file, goReads)),
  };
}

/** What the Go code reads outside backend/, as paths from the repository
 *  root: the files it embeds (`//go:embed`, the shared tables main.go
 *  embeds among them) and the ones its tests read from the frontend. A
 *  folder ends in "/" (a glob's folder stands for its files), and a test
 *  that names a file by its start ("src/help/agent-guide.") gives that. */
export function goReads(root = ownRoot) {
  const found = new Set();
  const add = (from, target) => {
    const path = posix.normalize(
      posix.join(from, target.replace(/[^/]*\*.*$/, "")),
    );
    if (!path.startsWith("backend/") && !path.startsWith("../"))
      found.add(path);
  };
  const scan = (dir, recurse) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (recurse && entry.name !== "testdata") scan(path, true);
        continue;
      }
      if (!entry.name.endsWith(".go")) continue;
      const from = relative(root, dir).split("\\").join("/") || ".";
      const text = readFileSync(path, "utf8");
      for (const [, list] of text.matchAll(/^\s*\/\/go:embed (.+)$/gm))
        for (const target of list.trim().split(/\s+/)) add(from, target);
      if (!entry.name.endsWith("_test.go")) continue;
      for (const [, target] of text.matchAll(
        /"((?:\.\.\/)+(?:src|e2e|docs)\/[^"]*)"/g,
      ))
        add(from, target);
      // backend/ai's tests name repository files from its root.
      for (const [, target] of text.matchAll(/readRepo\("([^"]+)"/g))
        found.add(target);
    }
  };
  scan(root, false);
  scan(join(root, "backend"), true);
  return [...found].sort();
}

function git(...args) {
  return execFileSync("git", args, {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

function show(rev, file) {
  try {
    return git("show", `${rev}:${file}`);
  } catch {
    return null;
  }
}

function main(argv) {
  const at = argv.indexOf("--base");
  const base = at === -1 ? "" : (argv[at + 1] ?? "");
  let result = {
    light: false,
    docs: false,
    native: true,
    roundtrip: true,
    full: false,
  };
  if (base && !/^0+$/.test(base)) {
    try {
      const files = git("diff", "--name-only", "--no-renames", base, "HEAD")
        .split("\n")
        .filter(Boolean);
      const push = argv.includes("--push");
      const read = (file) => [show(base, file), show("HEAD", file)];
      const reads = goReads();
      const jobs = classify({ files, push, read, goReads: reads });
      const full =
        !push &&
        files.includes("package.json") &&
        bumpsVersion(...read("package.json"));
      result = {
        ...jobs,
        // The full suite builds Go on every platform, as the tag will.
        native: jobs.native || full,
        docs: !push && docsOnly({ files, goReads: reads }),
        roundtrip: !jobs.light && reachesSaving({ files, push }),
        full,
      };
    } catch {
      // The base isn't in this clone (a force push): run everything.
    }
  }
  console.log(
    Object.entries(result)
      .map(([key, value]) => `${key}=${value}`)
      .join("\n"),
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
