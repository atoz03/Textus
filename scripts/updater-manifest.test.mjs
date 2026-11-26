import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  BETA_MANIFEST_NAME,
  betaAdvances,
  buildManifest,
  compareVersions,
  configProblems,
  forbiddenAssets,
  manifestProblems,
  MANIFEST_NAME,
  signingProblems,
} from "./updater-manifest.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const readRepo = (file) => readFileSync(join(root, file), "utf8");

/** A checkout's files: `files` where given, the repository's otherwise. */
const checkout =
  (files, fallback = readRepo) =>
  (file) =>
    file in files ? files[file] : fallback(file);

/** A tag of the Tauri builds, as `check --root` reads it: its updater
 *  endpoints, its beta endpoint and its release workflow. */
function tauriTag({
  endpoint = `https://github.com/CatVinci-Studio/Textus/releases/latest/download/${MANIFEST_NAME}`,
  beta = `https://textus.md/download/${BETA_MANIFEST_NAME}`,
  input = "uploadUpdaterJson",
  action = "v1.0.0",
} = {}) {
  const files = {
    "src-tauri/tauri.conf.json": JSON.stringify({
      plugins: { updater: { endpoints: [endpoint] } },
    }),
    "src-tauri/src/commands/update.rs": `const BETA_MANIFEST_URL: &str =\n    "${beta}";\n`,
    ".github/workflows/release.yml": [
      "      - uses: tauri-apps/tauri-action@1deb371b0cd8bd54025b384f1cd735e725c4060f # " +
        action,
      "        with:",
      ...(input ? [`          ${input}: false`] : []),
      "",
    ].join("\n"),
  };
  return checkout(files, (file) => {
    throw new Error(`ENOENT: ${file}`);
  });
}

describe("updater manifest", () => {
  it("this checkout reads textus-latest.json and never uploads latest.json", () => {
    expect(configProblems(readRepo)).toEqual([]);
  });

  it("rejects a Go app whose stable manifest Levis also reads", () => {
    const updater = readRepo("backend/updater.go");
    expect(updater).toContain(`/download/${MANIFEST_NAME}"`);
    const problems = configProblems(
      checkout({
        "backend/updater.go": updater.replace(
          `/${MANIFEST_NAME}"`,
          '/latest.json"',
        ),
      }),
    );
    expect(problems).toEqual([
      "backend/updater.go: stableManifestURL must end in /textus-latest.json",
    ]);
  });

  it("rejects a release workflow without the guard against latest.json", () => {
    const workflow = readRepo(".github/workflows/release.yml");
    const problems = configProblems(
      checkout({
        ".github/workflows/release.yml": workflow.replaceAll(
          "updater-manifest.mjs guard",
          "updater-manifest.mjs",
        ),
      }),
    );
    expect(problems.join("\n")).toMatch(
      /run `node scripts\/updater-manifest\.mjs guard`/,
    );
  });

  it("checks a Tauri tag as the Tauri builds were", () => {
    expect(configProblems(tauriTag())).toEqual([]);
  });

  it("rejects a Tauri endpoint that Levis also reads", () => {
    const problems = configProblems(
      tauriTag({
        endpoint:
          "https://github.com/CatVinci-Studio/Textus/releases/latest/download/latest.json",
      }),
    );
    expect(problems.join("\n")).toMatch(/must end in \/textus-latest\.json/);
  });

  it("rejects a release workflow that lets tauri-action upload latest.json", () => {
    const problems = configProblems(tauriTag({ input: null }));
    expect(problems.join("\n")).toMatch(/uploadUpdaterJson: false/);
  });

  it("rejects tauri-action 1.x with the input name from before 1.0", () => {
    const problems = configProblems(tauriTag({ input: "includeUpdaterJson" }));
    expect(problems.join("\n")).toMatch(
      /v1 must run with uploadUpdaterJson: false/,
    );
  });

  it("accepts the old input name on a tag that still pins tauri-action v0", () => {
    const problems = configProblems(
      tauriTag({ input: "includeUpdaterJson", action: "v0.6.2" }),
    );
    expect(problems).toEqual([]);
  });

  it("refuses to release a Go build that could never update itself", () => {
    const withUpdates = (updates) =>
      checkout({ "mygo.json": JSON.stringify({ name: "Textus", updates }) });
    expect(signingProblems(withUpdates(undefined))).toEqual([
      "mygo.json: no updates.publicKey (mygo keygen): a release built without it can never update itself",
    ]);
    expect(
      signingProblems(
        withUpdates({ publicKey: "bm90IGEga2V5", github: "a/b" }),
      ),
    ).toEqual([
      "mygo.json: updates.publicKey is not an Ed25519 public key (mygo keygen)",
    ]);
    const publicKey = Buffer.alloc(32, 7).toString("base64");
    expect(signingProblems(withUpdates({ publicKey }))).toHaveLength(1);
    expect(
      signingProblems(
        withUpdates({ publicKey, github: "CatVinci-Studio/Textus" }),
      ),
    ).toEqual([]);
  });

  it("refuses any release that carries latest.json", () => {
    expect(
      forbiddenAssets([MANIFEST_NAME, "Textus_1.0.0_x64-setup.exe", ""]),
    ).toEqual([]);
    expect(forbiddenAssets(["latest.json", MANIFEST_NAME])).toEqual([
      "latest.json is on the release",
    ]);
    expect(forbiddenAssets(["latest.json\r"])).toHaveLength(1);
  });

  // A release's MyGo updates, as `mygo build` writes update-<target>.json.
  const goBase =
    "https://github.com/CatVinci-Studio/Textus/releases/download/v1.5.0";
  const update = (target, extra = {}) => ({
    target,
    manifest: {
      version: "1.5.0",
      notes: "built in",
      date: "2026-12-01T00:00:00Z",
      url: `https://example.invalid/v1.5.0/textus-1.5.0-${target}.tar.gz`,
      size: 1000 + target.length,
      signature: `sig ${target}`,
      previous: [{ version: "1.4.9", url: "x", size: 1, signature: "y" }],
      ...extra,
    },
  });
  const goTargets = [
    "darwin-arm64",
    "darwin-amd64",
    "linux-amd64",
    "linux-arm64",
    "windows-amd64",
    "windows-arm64",
  ];
  const goRelease = buildManifest({
    version: "1.5.0",
    notes: "notes",
    pubDate: "2026-12-01T00:00:00.000Z",
    baseUrl: `${goBase}/`,
    updates: goTargets.map((target) =>
      target === "linux-amd64"
        ? update(target, {
            deltas: [
              {
                from: "1.4.9",
                url: "https://example.invalid/v1.5.0/textus-1.4.9-to-1.5.0-linux-amd64.delta",
                size: 10,
                signature: "delta sig",
              },
            ],
          })
        : update(target),
    ),
  });

  it("gives each Go build MyGo's update of its target, on the release", () => {
    expect(Object.keys(goRelease.platforms).sort()).toEqual(
      goTargets.map((target) => `mygo-${target}`).sort(),
    );
    expect(goRelease.platforms["mygo-windows-arm64"]).toEqual({
      url: `${goBase}/textus-1.5.0-windows-arm64.tar.gz`,
      size: 1000 + "windows-arm64".length,
      signature: "sig windows-arm64",
    });
    expect(goRelease.platforms["mygo-linux-amd64"].deltas).toEqual([
      {
        from: "1.4.9",
        url: `${goBase}/textus-1.4.9-to-1.5.0-linux-amd64.delta`,
        size: 10,
        signature: "delta sig",
      },
    ]);
    expect(goRelease.version).toBe("1.5.0");
    expect(goRelease.notes).toBe("notes");
    expect(manifestProblems(goRelease, "1.5.0")).toEqual([]);
  });

  it("serves both Macs from a universal build", () => {
    const universal = buildManifest({
      version: "1.5.0",
      notes: "",
      pubDate: "",
      baseUrl: goBase,
      updates: [
        "darwin-universal",
        "linux-amd64",
        "linux-arm64",
        "windows-amd64",
        "windows-arm64",
      ].map((target) => update(target)),
    });
    expect(manifestProblems(universal, "1.5.0")).toEqual([]);
  });

  it("refuses an update of another version, or an entry without its size", () => {
    expect(() =>
      buildManifest({
        version: "1.5.1",
        notes: "",
        pubDate: "",
        baseUrl: goBase,
        updates: [update("linux-amd64")],
      }),
    ).toThrow(/update-linux-amd64\.json is version 1\.5\.0, not 1\.5\.1/);
    const unsized = structuredClone(goRelease);
    delete unsized.platforms["mygo-linux-arm64"].size;
    expect(manifestProblems(unsized, "1.5.0")).toEqual([
      "no signed mygo-linux-arm64 entry",
    ]);
  });

  // A Tauri release's signed assets, named as tauri-bundler names them.
  const base =
    "https://github.com/CatVinci-Studio/Textus/releases/download/v1.1.0";
  const stable = buildManifest({
    version: "1.1.0",
    notes: "notes",
    pubDate: "2026-10-14T00:00:00.000Z",
    baseUrl: `${base}/`,
    artifacts: [
      { name: "Textus_aarch64.app.tar.gz", signature: "mac\n" },
      { name: "Textus_x64.app.tar.gz", signature: "intel" },
      { name: "Textus_1.1.0_amd64.AppImage", signature: "appimage" },
      { name: "Textus_1.1.0_amd64.deb", signature: "deb" },
      { name: "Textus-1.1.0-1.x86_64.rpm", signature: "rpm" },
      { name: "Textus_1.1.0_x64-setup.exe", signature: "nsis" },
      { name: "Textus_1.1.0_aarch64.AppImage", signature: "arm appimage" },
      { name: "Textus_1.1.0_arm64.deb", signature: "arm deb" },
      { name: "Textus-1.1.0-1.aarch64.rpm", signature: "arm rpm" },
      { name: "Textus_1.1.0_arm64-setup.exe", signature: "arm nsis" },
      { name: "Textus_1.1.0_linux_x86_64.tar.gz", signature: "ignored" },
    ],
  });
  const signed = (key) => stable.platforms[key]?.signature;

  it("builds every platform's entry from the signed artifacts", () => {
    expect(stable.platforms["darwin-aarch64"]).toEqual({
      signature: "mac",
      url: `${base}/Textus_aarch64.app.tar.gz`,
    });
    expect(Object.keys(stable.platforms).sort()).toEqual([
      "darwin-aarch64",
      "darwin-aarch64-app",
      "darwin-x86_64",
      "darwin-x86_64-app",
      "linux-aarch64",
      "linux-aarch64-appimage",
      "linux-aarch64-deb",
      "linux-aarch64-rpm",
      "linux-x86_64",
      "linux-x86_64-appimage",
      "linux-x86_64-deb",
      "linux-x86_64-rpm",
      "windows-aarch64",
      "windows-aarch64-nsis",
      "windows-x86_64",
      "windows-x86_64-nsis",
    ]);
  });

  it("keeps every x64 key on the x64 installers", () => {
    expect(signed("darwin-x86_64")).toBe("intel");
    expect(signed("darwin-x86_64-app")).toBe("intel");
    expect(signed("linux-x86_64")).toBe("appimage");
    expect(signed("linux-x86_64-appimage")).toBe("appimage");
    expect(signed("linux-x86_64-deb")).toBe("deb");
    expect(signed("linux-x86_64-rpm")).toBe("rpm");
    expect(signed("windows-x86_64")).toBe("nsis");
    expect(signed("windows-x86_64-nsis")).toBe("nsis");
  });

  it("serves ARM64 Windows and Linux their own installers", () => {
    expect(signed("linux-aarch64")).toBe("arm appimage");
    expect(signed("linux-aarch64-appimage")).toBe("arm appimage");
    expect(signed("linux-aarch64-deb")).toBe("arm deb");
    expect(signed("linux-aarch64-rpm")).toBe("arm rpm");
    expect(signed("windows-aarch64")).toBe("arm nsis");
    expect(stable.platforms["windows-aarch64-nsis"]).toEqual({
      signature: "arm nsis",
      url: `${base}/Textus_1.1.0_arm64-setup.exe`,
    });
  });

  it("refuses two artifacts for one platform", () => {
    expect(() =>
      buildManifest({
        version: "1.1.0",
        notes: "",
        pubDate: "",
        baseUrl: base,
        artifacts: [
          { name: "Textus_1.1.0_arm64-setup.exe", signature: "a" },
          { name: "Textus_1.1.0_aarch64-setup.exe", signature: "b" },
        ],
      }),
    ).toThrow(/two artifacts serve windows-aarch64/);
  });

  it("reports a missing platform or a version that is not the tag's", () => {
    const manifest = buildManifest({
      version: "1.5.0",
      notes: "",
      pubDate: "",
      baseUrl: goBase,
      updates: [update("windows-amd64")],
      artifacts: [{ name: "Textus_1.0.0_x64-setup.exe", signature: "s" }],
    });
    expect(manifestProblems(manifest, "1.5.1")).toEqual([
      "manifest says 1.5.0, expected 1.5.1",
      "no signed mygo-darwin-arm64 entry",
      "no signed mygo-darwin-amd64 entry",
      "no signed mygo-linux-amd64 entry",
      "no signed mygo-linux-arm64 entry",
      "no signed mygo-windows-arm64 entry",
    ]);
  });
});

describe("beta manifest", () => {
  const rcBase =
    "https://github.com/CatVinci-Studio/Textus/releases/download/v1.0.5-rc.1";
  const rc = buildManifest({
    version: "1.0.5-rc.1",
    notes: "",
    pubDate: "2026-10-20T00:00:00.000Z",
    baseUrl: rcBase,
    updates: [
      "darwin-arm64",
      "darwin-amd64",
      "linux-amd64",
      "linux-arm64",
      "windows-amd64",
      "windows-arm64",
    ].map((target) => ({
      target,
      manifest: {
        version: "1.0.5-rc.1",
        url: `${rcBase}/textus-1.0.5-rc.1-${target}.tar.gz`,
        size: 1,
        signature: target,
      },
    })),
  });
  const manifest = (version) => ({ version, platforms: {} });

  it("builds a complete manifest for a release candidate", () => {
    expect(manifestProblems(rc, "1.0.5-rc.1")).toEqual([]);
    expect(rc.platforms["mygo-windows-amd64"].url).toBe(
      `${rcBase}/textus-1.0.5-rc.1-windows-amd64.tar.gz`,
    );
  });

  it("orders versions as semver does", () => {
    const sorted = [
      "1.0.5",
      "1.0.4",
      "1.0.5-rc.10",
      "1.0.5-rc.2",
      "1.1.0-rc.1",
      "1.0.5-rc.1",
      "1.0.10",
      "1.0.5-beta.1",
    ].sort(compareVersions);
    expect(sorted).toEqual([
      "1.0.4",
      "1.0.5-beta.1",
      "1.0.5-rc.1",
      "1.0.5-rc.2",
      "1.0.5-rc.10",
      "1.0.5",
      "1.0.10",
      "1.1.0-rc.1",
    ]);
    expect(compareVersions("v1.0.5", "1.0.5")).toBe(0);
    expect(() => compareVersions("latest", "1.0.5")).toThrow();
  });

  it("starts from any release when there is no beta manifest yet", () => {
    expect(betaAdvances(null, rc)).toBe(true);
  });

  it("moves forward to a newer release candidate or stable release", () => {
    expect(betaAdvances(manifest("1.0.4"), rc)).toBe(true);
    expect(betaAdvances(rc, manifest("1.0.5-rc.2"))).toBe(true);
    expect(betaAdvances(manifest("1.0.5-rc.2"), manifest("1.0.5"))).toBe(true);
  });

  it("never moves back when an older tag is rebuilt", () => {
    expect(betaAdvances(manifest("1.0.5"), rc)).toBe(false);
    expect(betaAdvances(manifest("1.0.5-rc.2"), rc)).toBe(false);
    expect(betaAdvances(manifest("1.0.6-rc.1"), manifest("1.0.5"))).toBe(false);
  });

  it("refreshes the same version, whose rebuild re-signed its files", () => {
    expect(betaAdvances(rc, structuredClone(rc))).toBe(true);
  });

  it("this checkout's beta endpoint names textus-beta.json", () => {
    const updater = readRepo("backend/updater.go");
    expect(updater).toContain(`/download/${BETA_MANIFEST_NAME}"`);
    const problems = configProblems(
      checkout({
        "backend/updater.go": updater.replaceAll(
          BETA_MANIFEST_NAME,
          "latest.json",
        ),
      }),
    );
    expect(problems).toEqual([
      "backend/updater.go: betaManifestURL must end in /textus-beta.json",
    ]);
  });

  it("rejects a Tauri tag's beta endpoint that isn't textus-beta.json", () => {
    const problems = configProblems(
      tauriTag({ beta: "https://x/latest.json" }),
    );
    expect(problems.join("\n")).toMatch(/must end in \/textus-beta\.json/);
  });

  it("checks a tag from before the update channel without its endpoint", () => {
    const tag = tauriTag();
    const problems = configProblems((file) => {
      if (file.endsWith("update.rs")) throw new Error("ENOENT");
      return tag(file);
    });
    expect(problems).toEqual([]);
  });
});
