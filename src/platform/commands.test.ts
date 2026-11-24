import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/** The text of every non-test source file under `dir` with one of `exts`. */
function texts(dir: string, exts: RegExp, test: RegExp): string[] {
  return readdirSync(dir, { recursive: true, encoding: "utf8" })
    .filter((file) => exts.test(file) && !test.test(file))
    .map((file) => readFileSync(join(dir, file), "utf8"));
}

/** `source` from `start` to the closing brace on a line of its own. */
function block(source: string, start: string): string {
  const at = source.indexOf(start);
  return at < 0 ? "" : source.slice(at, source.indexOf("\n}\n", at));
}

const names = (text: string, re: RegExp) =>
  [...text.matchAll(re)].map((match) => match[1]!);

describe("the backend's commands", () => {
  it("each have a handler for every command the frontend calls", () => {
    // Through the bindings, or straight through native/ (window.ts's run).
    const called = new Set(
      texts("src/platform", /\.tsx?$/, /\.test\.tsx?$/).flatMap((text) =>
        names(
          text,
          /\b(?:__INVOKE|invoke|run)(?:<.*?>)?\("([a-z][a-z0-9_]*)"/g,
        ),
      ),
    );
    const go = texts("backend", /\.go$/, /_test\.go$/).join("\n");
    const stubs = readFileSync("backend/stubs.go", "utf8");
    const handled = new Set([
      ...names(go, /\b(?:command|windowCommand)\("([a-z0-9_]+)"/g),
      // What this build refuses, and answers with nothing.
      ...names(block(stubs, "var unsupportedCommands"), /"([a-z0-9_]+)"/g),
      ...names(block(stubs, "var emptyAnswers"), /^\s*"([a-z0-9_]+)":/gm),
    ]);
    expect(called.size).toBeGreaterThan(300);
    expect([...called].filter((name) => !handled.has(name))).toEqual([]);
  });
});
