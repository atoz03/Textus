#!/usr/bin/env node
// The in-app updater's manifest, and the guard that keeps Textus releases
// away from Levis installs: Levis polls `latest.json` through the renamed
// repository and trusts the same key, so a Textus release never carries one.
//
// textus-latest.json has an entry per platform. A Go build's is MyGo's
// update of its target, under `mygo-<GOOS>-<GOARCH>`: the url, size,
// signature and deltas of the update-<target>.json that `mygo build` writes
// beside the signed archive (backend/updater.go reads them). The Tauri
// builds' entries (darwin-aarch64, linux-x86_64-deb, ...) came from the
// minisign .sig files tauri-action uploaded.
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** What Textus's updater reads and the release workflow uploads. */
export const MANIFEST_NAME = "textus-latest.json";
/** What Levis's updater reads. Never on a Textus release. */
export const LEVIS_MANIFEST_NAME = "latest.json";
/** What the beta channel reads: the newest release of any kind, kept on the
 *  download bucket since GitHub's releases/latest skips pre-releases. */
export const BETA_MANIFEST_NAME = "textus-beta.json";
/** Where the Go app names both manifests' URLs. */
const GO_UPDATER = "backend/updater.go";
/** Where the Tauri builds named the beta manifest's URL. */
const BETA_ENDPOINT_SOURCE = "src-tauri/src/commands/update.rs";

const ownRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

function readOrNull(readFile, file) {
  try {
    return readFile(file);
  } catch {
    return null;
  }
}

/** Problems with the updater wiring in a checkout, one line each: the
 *  manifests the app reads, and what the release workflow uploads. A tag
 *  from the Tauri builds (src-tauri/) is checked as those were. */
export function configProblems(readFile) {
  const tauriConf = readOrNull(readFile, "src-tauri/tauri.conf.json");
  return [
    ...(tauriConf === null
      ? goEndpointProblems(readFile)
      : tauriEndpointProblems(readFile, tauriConf)),
    ...workflowProblems(readFile(".github/workflows/release.yml")),
  ];
}

/** The Go app's manifest URLs (backend/updater.go). */
function goEndpointProblems(readFile) {
  const source = readFile(GO_UPDATER);
  const problems = [];
  for (const [name, file] of [
    ["stableManifestURL", MANIFEST_NAME],
    ["betaManifestURL", BETA_MANIFEST_NAME],
  ]) {
    const url = new RegExp(`\\b${name}\\s*=\\s*"([^"]+)"`).exec(source)?.[1];
    if (!url?.endsWith(`/${file}`))
      problems.push(`${GO_UPDATER}: ${name} must end in /${file}`);
  }
  return problems;
}

/** A Tauri tag's endpoints: tauri.conf.json's, and the beta manifest's in
 *  update.rs. */
function tauriEndpointProblems(readFile, tauriConf) {
  const problems = [];
  const conf = JSON.parse(tauriConf);
  const endpoints = conf.plugins?.updater?.endpoints ?? [];
  if (endpoints.length === 0)
    problems.push("tauri.conf.json: no updater endpoints");
  for (const url of endpoints) {
    const file = url.split("/").pop();
    if (file !== MANIFEST_NAME)
      problems.push(
        `tauri.conf.json: updater endpoint ${url} must end in /${MANIFEST_NAME}`,
      );
  }
  // Tags from before the update channel have no beta endpoint to check.
  const updateRs = readOrNull(readFile, BETA_ENDPOINT_SOURCE);
  if (updateRs !== null) {
    const url = /BETA_MANIFEST_URL: &str =\s*"([^"]+)"/.exec(updateRs)?.[1];
    if (!url?.endsWith(`/${BETA_MANIFEST_NAME}`))
      problems.push(
        `${BETA_ENDPOINT_SOURCE}: BETA_MANIFEST_URL must end in /${BETA_MANIFEST_NAME}`,
      );
  }
  return problems;
}

/** The release workflow: one that builds with tauri-action must keep it
 *  from uploading latest.json; any must refuse a draft that carries one
 *  (`guard`) before publishing it. */
function workflowProblems(workflow) {
  if (!/tauri-apps\/tauri-action@/.test(workflow)) {
    return /updater-manifest\.mjs guard\b/.test(workflow)
      ? []
      : [
          `release.yml: run \`node scripts/updater-manifest.mjs guard\` on the draft's assets before publishing (it refuses ${LEVIS_MANIFEST_NAME})`,
        ];
  }
  const problems = [];
  // tauri-action 1.0 renamed the input and silently ignores the old one,
  // which would then upload latest.json; older tags pin v0.
  const major = Number(
    workflow.match(/tauri-apps\/tauri-action@\S+\s*#\s*v(\d+)/)?.[1] ?? NaN,
  );
  const input = major >= 1 ? "uploadUpdaterJson" : "includeUpdaterJson";
  if (Number.isNaN(major))
    problems.push(
      "release.yml: pin tauri-action to a commit with its version in a comment (`@<sha> # vX.Y.Z`)",
    );
  else if (!new RegExp(`^\\s*${input}:\\s*false\\s*$`, "m").test(workflow))
    problems.push(
      `release.yml: tauri-action v${major} must run with ${input}: false (it uploads ${LEVIS_MANIFEST_NAME} otherwise)`,
    );
  return problems;
}

/** Why a release built from this checkout couldn't update itself: MyGo
 *  builds its updater in only with `updates` in mygo.json, whose public
 *  key every later update must be signed with (`mygo keygen`; the private
 *  key is the release's MYGO_UPDATER_PRIVATE_KEY). */
export function signingProblems(readFile) {
  const updates = JSON.parse(readFile("mygo.json")).updates;
  if (!updates?.publicKey)
    return [
      "mygo.json: no updates.publicKey (mygo keygen): a release built without it can never update itself",
    ];
  if (Buffer.from(String(updates.publicKey), "base64").length !== 32)
    return [
      "mygo.json: updates.publicKey is not an Ed25519 public key (mygo keygen)",
    ];
  if (!updates.github && !updates.url)
    return [
      "mygo.json: updates needs github (owner/name), where the release's update archives are",
    ];
  return [];
}

/** The updater's `{arch}` (x86_64 or aarch64) for an artifact's name, which
 *  tauri-bundler spells differently per format. */
function artifactArch(name) {
  if (/[_.](x64|amd64|x86_64)[-_.]/.test(name)) return "x86_64";
  if (/[_.](arm64|aarch64)[-_.]/.test(name)) return "aarch64";
  return null;
}

/** The updater platform keys an artifact serves. `<os>-<arch>-<installer>`
 *  keeps a .deb or .rpm install on its format; the updater tries it first. */
export function platformKeys(name) {
  const arch = artifactArch(name);
  if (arch === null) return [];
  if (/\.app\.tar\.gz$/.test(name))
    return [`darwin-${arch}`, `darwin-${arch}-app`];
  if (/\.AppImage$/.test(name))
    return [`linux-${arch}`, `linux-${arch}-appimage`];
  if (/\.deb$/.test(name)) return [`linux-${arch}-deb`];
  if (/\.rpm$/.test(name)) return [`linux-${arch}-rpm`];
  if (/-setup\.exe$/.test(name))
    return [`windows-${arch}`, `windows-${arch}-nsis`];
  return [];
}

/** The manifest for the Go builds' `updates` ({ target, manifest }, each
 *  an update-<target>.json of `mygo build`) and a Tauri release's signed
 *  `artifacts` ({ name, signature }). Every file it names is the release's,
 *  at `baseUrl`, where the workflow uploaded it. */
export function buildManifest({
  version,
  notes,
  pubDate,
  baseUrl,
  updates = [],
  artifacts = [],
}) {
  const base = baseUrl.replace(/\/$/, "");
  const onRelease = (name) => `${base}/${encodeURIComponent(name)}`;
  const fileOf = (url) =>
    decodeURIComponent(new URL(url).pathname).split("/").pop();
  const platforms = {};
  const add = (key, entry) => {
    if (platforms[key]) throw new Error(`two artifacts serve ${key}`);
    platforms[key] = entry;
  };
  for (const { target, manifest } of updates) {
    if (manifest.version !== version)
      throw new Error(
        `update-${target}.json is version ${manifest.version}, not ${version}`,
      );
    const entry = {
      url: onRelease(fileOf(manifest.url)),
      size: manifest.size,
      signature: manifest.signature,
    };
    if (manifest.deltas?.length)
      entry.deltas = manifest.deltas.map((delta) => ({
        ...delta,
        url: onRelease(fileOf(delta.url)),
      }));
    add(`mygo-${target}`, entry);
  }
  for (const { name, signature } of artifacts) {
    for (const key of platformKeys(name))
      add(key, { signature: signature.trim(), url: onRelease(name) });
  }
  return { version, notes, pub_date: pubDate, platforms };
}

/** The targets every release must serve, as MyGo names them; `build` and
 *  `beta` refuse a manifest lacking any. A universal macOS build serves
 *  both Macs (backend/updater.go `updateKeys`). */
export const REQUIRED_PLATFORMS = [
  "mygo-darwin-arm64",
  "mygo-darwin-amd64",
  "mygo-linux-amd64",
  "mygo-linux-arm64",
  "mygo-windows-amd64",
  "mygo-windows-arm64",
];

export function manifestProblems(manifest, version) {
  const problems = [];
  if (manifest.version !== version)
    problems.push(`manifest says ${manifest.version}, expected ${version}`);
  const signed = (entry) =>
    Boolean(entry?.url && entry?.signature && entry?.size > 0);
  for (const key of REQUIRED_PLATFORMS) {
    const universal = key.startsWith("mygo-darwin-")
      ? manifest.platforms["mygo-darwin-universal"]
      : undefined;
    if (!signed(manifest.platforms[key]) && !signed(universal))
      problems.push(`no signed ${key} entry`);
  }
  return problems;
}

/** Semver precedence of two versions: negative, zero or positive. Build
 *  metadata is ignored. */
export function compareVersions(a, b) {
  const parse = (v) => {
    const m =
      /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/.exec(
        String(v),
      );
    if (!m) throw new Error(`not a version: ${v}`);
    return { core: m.slice(1, 4).map(Number), pre: m[4]?.split(".") ?? [] };
  };
  const x = parse(a);
  const y = parse(b);
  for (let i = 0; i < 3; i++)
    if (x.core[i] !== y.core[i]) return x.core[i] - y.core[i];
  if (x.pre.length === 0 || y.pre.length === 0)
    return y.pre.length - x.pre.length;
  for (let i = 0; i < Math.max(x.pre.length, y.pre.length); i++) {
    const p = x.pre[i];
    const q = y.pre[i];
    if (p === undefined) return -1;
    if (q === undefined) return 1;
    if (p === q) continue;
    const pn = /^\d+$/.test(p);
    const qn = /^\d+$/.test(q);
    if (pn && qn) return Number(p) - Number(q);
    if (pn !== qn) return pn ? -1 : 1;
    return p < q ? -1 : 1;
  }
  return 0;
}

/** Whether the beta manifest moves from `current` (or null) to `candidate`.
 *  Never backwards; the same version replaces it, since a rebuild re-signs. */
export function betaAdvances(current, candidate) {
  if (!current?.version) return true;
  return compareVersions(candidate.version, current.version) >= 0;
}

/** Why a release with these assets can't be published: it carries
 *  latest.json, which every Levis install would read and install. */
export function forbiddenAssets(names) {
  return names.some((name) => name.trim() === LEVIS_MANIFEST_NAME)
    ? [`${LEVIS_MANIFEST_NAME} is on the release`]
    : [];
}

function fail(lines) {
  for (const line of lines) console.error(`::error::${line}`);
  process.exit(1);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [command, ...args] = process.argv.slice(2);
  const option = (name) => {
    const at = args.indexOf(name);
    if (at < 0) return undefined;
    if (!args[at + 1]) fail([`${name} needs a value`]);
    return args[at + 1];
  };
  const required = (name) => option(name) ?? fail([`${name} is required`]);

  if (command === "check") {
    const root = option("--root") ?? ownRoot;
    const read = (file) => readFileSync(join(root, file), "utf8");
    const problems = configProblems(read);
    // A Tauri tag signed its updates with tauri-action's key instead.
    if (readOrNull(read, "src-tauri/tauri.conf.json") === null)
      problems.push(...signingProblems(read));
    if (problems.length > 0) fail(problems);
  } else if (command === "build") {
    const version = required("--version");
    const updateDir = option("--update-dir");
    const sigDir = option("--sig-dir");
    if (!updateDir && !sigDir) fail(["--update-dir or --sig-dir is required"]);
    const notesFile = option("--notes-file");
    const updates = updateDir
      ? readdirSync(updateDir)
          .map((file) => /^update-(.+)\.json$/.exec(file))
          .filter(Boolean)
          .map(([file, target]) => ({
            target,
            manifest: JSON.parse(readFileSync(join(updateDir, file), "utf8")),
          }))
      : [];
    const artifacts = sigDir
      ? readdirSync(sigDir)
          .filter((file) => file.endsWith(".sig"))
          .map((file) => ({
            name: file.slice(0, -".sig".length),
            signature: readFileSync(join(sigDir, file), "utf8"),
          }))
      : [];
    const manifest = buildManifest({
      version,
      notes: notesFile ? readFileSync(notesFile, "utf8") : "",
      pubDate: new Date().toISOString(),
      baseUrl: required("--base-url"),
      updates,
      artifacts,
    });
    const problems = manifestProblems(manifest, version);
    if (problems.length > 0) fail(problems);
    writeFileSync(required("--out"), `${JSON.stringify(manifest, null, 2)}\n`);
  } else if (command === "beta") {
    const version = required("--version");
    const manifest = JSON.parse(readFileSync(required("--manifest"), "utf8"));
    const problems = manifestProblems(manifest, version);
    if (problems.length > 0) fail(problems);
    const currentFile = option("--current");
    const current = currentFile
      ? JSON.parse(readFileSync(currentFile, "utf8"))
      : null;
    console.log(betaAdvances(current, manifest) ? "advance" : "keep");
  } else if (command === "guard") {
    const names = readFileSync(required("--assets-file"), "utf8").split("\n");
    const bad = forbiddenAssets(names);
    if (bad.length > 0)
      fail([
        ...bad,
        `every Levis install reads ${LEVIS_MANIFEST_NAME} from this repository and trusts the same signing key, so it would install Textus over itself. Delete the asset from the draft and run the job again.`,
      ]);
  } else {
    fail([
      `unknown command ${command ?? "(none)"}: check, build, beta or guard`,
    ]);
  }
}
