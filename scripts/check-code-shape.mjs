#!/usr/bin/env node
// Checks the code shape rules: at most 500 lines per source file, at most 8
// hooks per component or custom hook, and no provider id compared outside
// its adapter.
// Files listed over a limit may shrink, not grow.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const MAX_LINES = 500;
const MAX_HOOKS = 8;

/** Over the line limit today: at most this many lines. */
const LINES_NOW = {};

/** Not held to the line limit: tables, not logic. */
const TABLES = [
  /^src\/i18n\/locales\//,
  /^src\/engine\/controls\/icons\.tsx$/,
  // The backend's command list, one line per command.
  /^src\/platform\/bindings\//,
];

function walk(dir, out = []) {
  for (const name of readdirSync(join(root, dir))) {
    const path = `${dir}/${name}`;
    if (name === "node_modules" || name === "target") continue;
    if (statSync(join(root, path)).isDirectory()) walk(path, out);
    else out.push(path);
  }
  return out;
}

const isTest = (path) =>
  /\.(test|spec)\.tsx?$/.test(path) ||
  /_test\.go$/.test(path) ||
  path.startsWith("src/dev/");

const sources = [
  ...walk("src").filter((p) => /\.tsx?$/.test(p)),
  // Plain scripts put into a plugin's frames as text.
  ...walk("src/plugins/host/runtime").filter((p) => p.endsWith(".js")),
  ...walk("backend").filter((p) => p.endsWith(".go")),
].filter((p) => !isTest(p));

const problems = [];

// Lines.
for (const path of sources) {
  if (TABLES.some((re) => re.test(path))) continue;
  const lines = readFileSync(join(root, path), "utf8").split("\n").length - 1;
  const cap = LINES_NOW[path] ?? MAX_LINES;
  if (lines > cap)
    problems.push(
      `${path}: ${lines} lines, over ${cap} - split it by concern (docs/dev/code-style.md, "Code shape")`,
    );
}

// Hooks per component, and per custom hook (`useX`, in .ts files too).
const COMPONENT =
  /^(?:export )?(?:default )?(?:function ([A-Z]\w*|use[A-Z]\w*)|const ([A-Z]\w*|use[A-Z]\w*) = (?:memo\()?(?:function \w*)?)/gm;
const NEXT_DECLARATION =
  /^(?:export )?(?:function|const|let|interface|type|class) [a-z_]/gm;
const HOOK = /(?<![\w.])use[A-Z]\w*\s*(?:<(?:[^<>]|<[^<>]*>)*>)?\s*\(/g;
for (const path of sources.filter((p) => /\.tsx?$/.test(p))) {
  const text = readFileSync(join(root, path), "utf8");
  const starts = [...text.matchAll(COMPONENT)].map((m) => ({
    at: m.index,
    name: m[1] ?? m[2],
  }));
  starts.forEach(({ at, name }, i) => {
    const end = i + 1 < starts.length ? starts[i + 1].at : text.length;
    NEXT_DECLARATION.lastIndex = at + 1;
    const next = NEXT_DECLARATION.exec(text);
    const body = text.slice(at, next && next.index < end ? next.index : end);
    // A custom hook's own name isn't one of its calls.
    const hooks = (body.match(HOOK)?.length ?? 0) - (/^use/.test(name) ? 1 : 0);
    if (hooks > MAX_HOOKS)
      problems.push(
        `${path}: ${name} calls ${hooks} hooks, over ${MAX_HOOKS} - move a concern into a hook of its own`,
      );
  });
}

// Provider ids compared in code. The ids are the catalog's (one line of
// JSON in the bindings); "custom" is the user's own endpoint, not a
// provider.
const catalog = readFileSync(join(root, "src/platform/bindings/ai.ts"), "utf8");
const catalogJson =
  /^export const PROVIDER_CATALOG = (\[.*\]) as const;$/m.exec(catalog)?.[1];
if (!catalogJson) throw new Error("bindings/ai.ts: no PROVIDER_CATALOG");
const ids = JSON.parse(catalogJson)
  .map((entry) => entry.id)
  .filter((id) => id !== "custom");
const alt = ids.map((id) => id.replace(/-/g, "\\-")).join("|");
const TS_COMPARE = new RegExp(
  `(?:===|!==)\\s*["'](?:${alt})["']|["'](?:${alt})["']\\s*(?:===|!==)|case\\s+["'](?:${alt})["']\\s*:`,
);
const GO_COMPARE = new RegExp(
  `(?:==|!=)\\s*"(?:${alt})"|"(?:${alt})"\\s*(?:==|!=)|case\\s+(?:"[^"]*",\\s*)*"(?:${alt})"`,
);
// A provider's own adapter is where its differences are written down: the
// Go backend has none yet (its AI is not ported).
const ADAPTERS = [/^backend\/ai\//];
for (const path of sources) {
  if (ADAPTERS.some((re) => re.test(path))) continue;
  const compare = path.endsWith(".go") ? GO_COMPARE : TS_COMPARE;
  readFileSync(join(root, path), "utf8")
    .split("\n")
    .forEach((line, i) => {
      const code = line.trim();
      if (code.startsWith("//") || code.startsWith("*")) return;
      if (compare.test(code))
        problems.push(
          `${path}:${i + 1}: compares a provider id - make the difference data`,
        );
    });
}

// Listeners kept by hand (a Set added to, deleted from, looped over):
// createSubscribers in src/utils/store.ts does that. Registries that run
// once or guard each call are not listeners.
const LISTENER_SET = /new Set<\((?:[^()]|\([^()]*\))*\) => void>\(\)/;
const REGISTRIES = new Set([
  "src/utils/store.ts",
  "src/utils/memory-idle.ts",
  "src/storage/pending-writes.ts",
  "src/document/reload-tabs.ts",
  "src/editor/math/katex-loader.ts",
]);
for (const path of sources) {
  if (!/\.tsx?$/.test(path) || REGISTRIES.has(path)) continue;
  readFileSync(join(root, path), "utf8")
    .split("\n")
    .forEach((line, i) => {
      if (LISTENER_SET.test(line))
        problems.push(
          `${path}:${i + 1}: listeners kept by hand - use createSubscribers`,
        );
    });
}

// A fixed pause in an end-to-end test says what real time it stands for
// (the undo grouping, a hold, a notice's life) on the line above; else it
// waits for what it means instead (settled, an expect.poll). The suite run on
// every pull request: not the benches, perf runs or screenshot scripts.
const PAUSE = /\.waitForTimeout\(/;
const OFF_SUITE = /^e2e\/(bench|perf|shots|frames)\//;
for (const path of walk("e2e").filter(
  (p) => /\.ts$/.test(p) && !OFF_SUITE.test(p),
)) {
  const lines = readFileSync(join(root, path), "utf8").split("\n");
  lines.forEach((line, i) => {
    if (PAUSE.test(line) && !/^\s*\/\//.test(lines[i - 1] ?? ""))
      problems.push(
        `${path}:${i + 1}: a fixed pause - wait for what it means (settled, expect.poll), or say on the line above what real time it is`,
      );
  });
}

if (problems.length > 0) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log(
  `code shape: ${sources.length} files within ${MAX_LINES} lines, components within ${MAX_HOOKS} hooks, no provider ids compared, listeners shared, e2e pauses explained`,
);
