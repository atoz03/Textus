import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  codePaths,
  isLocal,
  linkTargets,
  missingCodePaths,
  namesCode,
} from "./check-links.mjs";
import {
  bumpsVersion,
  classify,
  docsOnly,
  goReads,
  readByGo,
  reachesSaving,
} from "./ci-changes.mjs";

const VERSION_FILES = ["package.json", "package-lock.json", "mygo.json"];
const version = JSON.parse(readFileSync("package.json", "utf8")).version;
const current = Object.fromEntries(
  VERSION_FILES.map((file) => [file, readFileSync(file, "utf8")]),
);

/** The real files as a bump to 9.9.9 leaves them: only the app's own
 *  version lines. A dependency with the same version must stay as it is. */
const bumpedLock = JSON.parse(current["package-lock.json"]);
bumpedLock.version = "9.9.9";
bumpedLock.packages[""].version = "9.9.9";
const bumped = {
  "package.json": current["package.json"].replace(
    `"version": "${version}"`,
    '"version": "9.9.9"',
  ),
  "package-lock.json": `${JSON.stringify(bumpedLock, null, 2)}\n`,
  "mygo.json": current["mygo.json"].replace(
    `"version": "${version}"`,
    '"version": "9.9.9"',
  ),
};

const reads = goReads();

/** The jobs for a change from `before` to `after` (maps of file to text;
 *  files missing from either side don't exist there). */
function change(before, after, { push = false } = {}) {
  const files = [...new Set([...Object.keys(before), ...Object.keys(after)])];
  return classify({
    files,
    push,
    read: (file) => [before[file] ?? null, after[file] ?? null],
    goReads: reads,
  });
}

/** Each of `files` edited from "a" to "b". */
function edit(files, options) {
  const side = (text) => Object.fromEntries(files.map((f) => [f, text]));
  return change(side("a\n"), side("b\n"), options);
}

const NONE = { light: false, native: false };
const LIGHT = { light: true, native: false };
const NATIVE = { light: false, native: true };

describe("which CI jobs a pull request needs", () => {
  it("runs check alone for documentation outside the pictures", () => {
    expect(
      edit(["docs/dev/testing.md", "README.md", "packaging/arch/README.md"]),
    ).toEqual(NONE);
  });

  it("runs the light check alone for GitHub's settings, the license, root Markdown and the READMEs' pictures, merged too", () => {
    const files = [
      ".github/FUNDING.yml",
      ".github/CODEOWNERS",
      ".github/dependabot.yml",
      ".github/ISSUE_TEMPLATE/bug.yml",
      ".github/pull_request_template.md",
      "LICENSE",
      "CONTRIBUTING.md",
      "README.zh.md",
      "docs/images/readme/en/hero.png",
      "docs/images/appstore/zh/01.png",
    ];
    expect(edit(files)).toEqual(LIGHT);
    expect(edit(files, { push: true })).toEqual(LIGHT);
    for (const file of [
      ".github/workflows/ci.yml",
      ".github/actions/e2e-setup/action.yml",
      "docs/dev/plugins/reference.md",
      ".prettierrc",
      "knip.json",
    ])
      expect(edit([file]).light, file).toBe(false);
    expect(edit([".github/FUNDING.yml", "src/main.tsx"]).light).toBe(false);
    const embedded = classify({
      files: ["NOTES.md"],
      push: false,
      read: () => ["a\n", "b\n"],
      goReads: ["NOTES.md"],
    });
    expect(embedded).toEqual(NATIVE);
  });

  it("runs native for the Markdown the Go backend embeds", () => {
    expect(
      edit([
        "backend/ai/skills/proofread.md",
        "backend/ai/skills/packs/craft.md",
        "backend/plugins/template/README.md",
      ]),
    ).toEqual(NATIVE);
  });

  it("runs check alone for the Markdown the frontend imports", () => {
    expect(
      edit([
        "src/playground/samples/zh.md",
        "src/theme/fixtures/theme-skill-reply.md",
      ]),
    ).toEqual(NONE);
  });

  it("runs native for the help pages a Go test sweeps", () => {
    expect(edit(["src/help/markdown-guide.en.md"])).toEqual(NATIVE);
    expect(edit(["src/help/agent-guide.zh.md"])).toEqual(NATIVE);
  });

  it("runs native for Markdown outside backend/ that Go reads", () => {
    const change = classify({
      files: ["notes/prompt.md"],
      push: false,
      read: () => ["a\n", "b\n"],
      goReads: ["notes/prompt.md"],
    });
    expect(change.native).toBe(true);
  });

  it("runs check alone for a version bump", () => {
    expect(bumped["mygo.json"]).not.toBe(current["mygo.json"]);
    expect(change(current, bumped)).toEqual(NONE);
  });

  it("runs check alone for a frontend change", () => {
    expect(
      edit(["src/editor/editor.ts", "e2e/writing.spec.ts", "vite.config.ts"]),
    ).toEqual(NONE);
  });

  it("runs native alone for a Go change", () => {
    expect(
      edit([
        "backend/app.go",
        "backend/ai/aicompat/capabilities.go",
        "main.go",
        "go.mod",
        "go.sum",
        "scripts/check-native.mjs",
        "scripts/smoke-linux.sh",
      ]),
    ).toEqual(NATIVE);
  });

  it("runs native for the generated bindings, which a Go test compares", () => {
    expect(edit(["src/platform/bindings/app.ts"])).toEqual(NATIVE);
  });

  it("runs native for a frontend file the Go code embeds or its tests read", () => {
    expect(reads).toContain("package.json");
    expect(reads).toContain("src/registry/menu/menu.json");
    expect(edit(["src/registry/menu/menu.json"])).toEqual(NATIVE);
    expect(edit(["src/theme/content-themes.css"])).toEqual(NATIVE);
    expect(edit(["src/i18n/locales/fr.ts"])).toEqual(NATIVE);
  });

  it("runs native for what the app ships beside the Go code", () => {
    for (const file of [
      "backend/boot.js",
      "backend/icons/tray/tray-64.png",
      "mygo.json",
      "packaging/icons/icon.png",
      "packaging/windows/installer.nsh",
      "resources/windows/doc.ico",
    ]) {
      expect(edit([file]), file).toEqual(NATIVE);
    }
  });

  it("runs native for a change to both sides", () => {
    expect(edit(["src/main.tsx", "backend/app.go"])).toEqual(NATIVE);
  });

  it("runs native for a workflow or Node version change", () => {
    expect(edit([".github/workflows/ci.yml"])).toEqual(NATIVE);
    expect(edit([".nvmrc"])).toEqual(NATIVE);
  });

  it("ignores a bump's version lines in a mixed change", () => {
    const after = { ...bumped, "src/main.tsx": "b\n" };
    expect(change({ ...current, "src/main.tsx": "a\n" }, after)).toEqual(NONE);
  });

  it("runs native when the npm dependencies change", () => {
    const pkg = JSON.parse(bumped["package.json"]);
    pkg.dependencies["left-pad"] = "^1.3.0";
    const after = { ...bumped, "package.json": JSON.stringify(pkg, null, 2) };
    expect(change(current, after)).toEqual(NATIVE);
  });

  it("runs native when the lockfile adds a package", () => {
    const lock = JSON.parse(current["package-lock.json"]);
    lock.packages["node_modules/left-pad"] = { version: "1.3.0" };
    const file = "package-lock.json";
    expect(
      change(
        { [file]: current[file] },
        { [file]: JSON.stringify(lock, null, 2) },
      ),
    ).toEqual(NATIVE);
  });

  it("runs native when a bump also changes MyGo's configuration", () => {
    const config = JSON.parse(bumped["mygo.json"]);
    config.urlSchemes = [...config.urlSchemes, "textus-dev"];
    const after = { ...bumped, "mygo.json": JSON.stringify(config, null, 2) };
    expect(change(current, after)).toEqual(NATIVE);
  });

  it("runs native when the Go module moves a dependency", () => {
    const file = "go.sum";
    expect(
      change(
        { [file]: "example.com/a v1.0.0 h1:x=\n" },
        { [file]: "example.com/a v1.0.1 h1:y=\n" },
      ),
    ).toEqual(NATIVE);
  });

  it("runs native when a version file is added or removed", () => {
    expect(change({}, { "package.json": "{}" })).toEqual(NATIVE);
    expect(change({ "package.json": "{}" }, {})).toEqual(NATIVE);
  });
});

describe("which CI jobs a push needs", () => {
  it("runs check alone for a version bump", () => {
    expect(change(current, bumped, { push: true })).toEqual(NONE);
  });

  it("runs native otherwise, documentation and Go alone included", () => {
    for (const file of ["docs/dev/testing.md", "backend/app.go"]) {
      expect(edit([file], { push: true }), file).toEqual(NATIVE);
    }
  });
});

describe("documentation alone and the version bump", () => {
  it("takes Markdown outside the code as documentation alone", () => {
    expect(
      docsOnly({ files: ["docs/dev/testing.md", "docs/releases/1.4.4.md"] }),
    ).toBe(true);
    expect(docsOnly({ files: ["CONTRIBUTING.md"] })).toBe(false); // light
    expect(docsOnly({ files: ["docs/dev/textus.d.ts"] })).toBe(false);
    expect(docsOnly({ files: ["docs/dev/guide.md", "src/main.tsx"] })).toBe(
      false,
    );
    expect(docsOnly({ files: ["src/help/markdown-guide.en.md"] })).toBe(false);
    expect(docsOnly({ files: ["docs/x.md"], goReads: ["docs/x.md"] })).toBe(
      false,
    );
    expect(
      docsOnly({ files: ["docs/x/guide.en.md"], goReads: ["docs/x/guide."] }),
    ).toBe(false);
  });

  it("matches what Go reads by file, by folder and by a name's start", () => {
    const list = ["mygo.json", "src/help/", "src/help/agent-guide."];
    expect(readByGo("mygo.json", list)).toBe(true);
    expect(readByGo("src/help/markdown-guide.en.md", list)).toBe(true);
    expect(readByGo("src/helpers.ts", list)).toBe(false);
    expect(readByGo("mygo.json.bak", list)).toBe(false);
  });

  it("finds what the Go code embeds and its tests read outside backend/", () => {
    expect(reads).toEqual(
      expect.arrayContaining([
        "package.json", // main.go embeds it
        "src/platform/bindings/", // a test globs the bindings
        "src/help/agent-guide.", // a test names each language's guide
        "mygo.json", // a test reads the app's name from it
      ]),
    );
    expect(reads.filter((path) => path.startsWith("backend/"))).toEqual([]);
  });

  it("finds a bump in package.json's version alone", () => {
    expect(bumpsVersion(current["package.json"], bumped["package.json"])).toBe(
      true,
    );
    expect(bumpsVersion(current["package.json"], current["package.json"])).toBe(
      false,
    );
    expect(bumpsVersion(null, current["package.json"])).toBe(false);
  });

  it("follows relative links only, outside code", () => {
    expect(
      linkTargets(
        '[a](../b.md#c) `[x](y.md)` ![p](pic.png "t")\n```\n[z](z.md)\n```\n',
      ),
    ).toEqual(["../b.md#c", "pic.png"]);
    expect(
      ["../b.md", "https://x.y", "#top", "/abs", "mailto:a@b"].map(isLocal),
    ).toEqual([true, false, false, false, false]);
  });

  it("finds the source files a doc names in code spans, outside fences", () => {
    expect(
      codePaths(
        "`src/app/App.tsx` `lib.rs:42` `settings.json` `npm run x`\n```\n`gone.ts`\n```\n",
      ),
    ).toEqual(["src/app/App.tsx", "lib.rs"]);
  });

  it("names a doc's source file missing unless the repository has it", () => {
    const dir = mkdtempSync(join(tmpdir(), "doc-paths-"));
    writeFileSync(
      join(dir, "a.md"),
      "`editor/x/doc.ts` `doc.ts` `src/gone.ts` `kebab-case.ts` `old.rs`",
    );
    expect(missingCodePaths(["a.md"], ["src/editor/x/doc.ts"], dir)).toEqual([
      "a.md: src/gone.ts",
      "a.md: old.rs",
    ]);
    expect(
      [
        "AGENTS.md",
        "docs/dev/a.md",
        "docs/dev/plugins/a.md",
        "docs/releases/1.0.0.md",
      ].map(namesCode),
    ).toEqual([true, true, false, false]);
  });
});

describe("the save round trip in CI", () => {
  const runs = (...files) => reachesSaving({ files, push: false });

  it("runs for a change to the editor, the document code or the dependencies", () => {
    expect(runs("src/editor/markdown/markdown-output.ts")).toBe(true);
    expect(runs("src/document-view/useDocumentReporting.ts")).toBe(true);
    expect(runs("src/document/file-format.ts")).toBe(true);
    expect(runs("package-lock.json")).toBe(true);
  });

  it("runs for a change to the documents it reads", () => {
    expect(runs("docs/design/ai-eval.md")).toBe(true);
    expect(runs("src/help/markdown-guide.en.md")).toBe(true);
  });

  it("skips a change that can't reach saving", () => {
    expect(runs("backend/app.go")).toBe(false);
    expect(runs("src/sidebar/Sidebar.tsx", "README.md")).toBe(false);
  });

  it("always runs on a push", () => {
    expect(reachesSaving({ files: ["README.md"], push: true })).toBe(true);
  });
});
