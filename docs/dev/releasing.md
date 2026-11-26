# Releasing

How work reaches `main`, how a test build and a release are cut, and what
the release workflows do. This page is the one place for the branch,
pull request and commit rules; other documents link here.

The desktop app ships from a `vX.Y.Z` tag through
`.github/workflows/release.yml`, which builds it with MyGo (`mygo build`,
configured in `mygo.json`); the iOS app ships from an `ios-vX.Y.Z` tag
through `.github/workflows/ios-release.yml` (see [ios-release.md](ios-release.md)),
still from the Tauri sources at `8da2d2f0`.
The browser playground isn't released: it is deployed after each newest
stable release ([infrastructure.md](infrastructure.md#playground)).

## Branches

There is one branch, `main`, where all work lands. Every version is a tag
on it:

- **Test builds** are `vX.Y.Z-rc.N`, published as GitHub pre-releases and
  offered on the Beta update channel ([Pre-releases](#pre-releases)).
- **Releases** are `vX.Y.Z` on the merged commit that sets the version
  ([Desktop release](#desktop-release)).
- There are no patch releases from an older tag: a fix the released version
  needs lands on `main` and ships in the next version.
- Never push to `main` directly. Branch protection requires
  the `check`, `e2e` and `native` CI jobs (and `ci-config`, which only a
  fork's pull request runs). It doesn't require the branch to be up to
  date: a pull request that passed is merged as it is, and the CI run after
  the merge catches the rare case where two changes only break together.
- Older versions are kept as tags (`v1.2.8`, `v1.1.2`; `v0.9.8` for Levis),
  not branches. The `stable` branch that carried releases until 1.4.2 is
  kept as the tag `archive/stable`.
- Only owners push `v*` and `ios-v*` tags.

## Pull requests and commits

- **One pull request per repository per release.** Maintainers batch the
  work for a release into one branch and one pull request to `main`; its
  steps are commits in it. Don't open a pull request per change, and don't
  stack pull requests. A contributor's pull request goes to `main` as it
  is: we take it from there.
- **Pull requests are squash-merged.** The pull request's title and
  description become the one commit on `main`, so write them as a commit
  message. Merged branches are deleted automatically.
- **Commit messages are plain English sentences** that say what changed,
  not Conventional Commits:

  ```
  Preview tabs: a single click in the file tree opens a file to look at
  Source mode edits as plain text with colours; Enter still continues a list
  ```

  The body, when there is one, says why the old behaviour was wrong: the
  failure it prevents, not a restatement of the diff. No "generated with"
  trailers, and no `Co-authored-by` except to credit an outside contributor
  whose work the commit carries.

- Name a branch after what it does: `fix/pdf-margins`, `feat/sidebar-width`,
  `docs/dev-1310`. Branch from `origin/main`; if `main` moves on while you
  work, merge it into your branch.
- Each pull request adds its release note as a fragment in
  `docs/releases/next/` ([its README](../releases/next/README.md)), never
  into `docs/releases/X.Y.Z.md`.

Both kinds of tag can be published in the same week. A stable tag becomes
GitHub's "Latest" release - the one the updater, the website's downloads,
Homebrew, the mirror and the Arch repository follow - only when it is the
highest stable version (release.yml's "Is this the newest stable tag?"),
and a pre-release never does. The beta channel's manifest only moves
forward (`scripts/updater-manifest.mjs beta`).

## Desktop release

A test build and a release run the same workflow; they differ in the
version and the notes. Both are tagged on `main`.

### A test build (`vX.Y.Z-rc.N`)

Before either kind, in the release's pull request:

- **Plugins.** If the official plugins changed, merge their pull request
  first, then point CI at it: `node scripts/pin-plugins.mjs` (textus-plugins'
  `main`; `--check` prints the pin). The e2e flows test the plugins at that
  commit.
- **What CI doesn't block on.** Look at the latest `main` run's WebKit job
  and at any test reported flaky (testing.md › Flaky tests); fix or file
  each.
- **Speed.** When the release touches the editor, large documents or plugin
  loading, run the benchmarks on the same machine as last time and compare.

1. In the release's pull request to `main`, set the version to
   `X.Y.Z-rc.N` in `package.json` and `mygo.json`, then run
   `npm install --package-lock-only` so `package-lock.json` (both of its
   version fields) follows. The Go backend takes its version from the
   `package.json` it embeds. `node scripts/check-versions.mjs` (part of
   `npm run check`) fails until the three files agree. A pull request that
   changes the version runs the full CI suite on every platform by itself.
2. Once it is merged, tag that exact commit on `main`:

   ```sh
   git fetch origin
   node scripts/check-versions.mjs --tag vX.Y.Z-rc.N   # on a checkout of origin/main
   git tag -a vX.Y.Z-rc.N origin/main -m "Textus X.Y.Z-rc.N"
   git push origin vX.Y.Z-rc.N
   ```

Release notes aren't required for a pre-release: the fragments stay in
`docs/releases/next/` until the stable release collects them.

### A release (`vX.Y.Z`)

1. On `main`, set the version to `X.Y.Z` as in step 1 above, and collect
   the release notes: `node scripts/release-notes.mjs build X.Y.Z` adds
   every fragment in `docs/releases/next/` to `docs/releases/X.Y.Z.md`
   (creating it in the outline of [TEMPLATE.md](../releases/TEMPLATE.md)
   if it doesn't exist), each bullet at the end of its section, oldest
   first, and deletes the fragments. Then edit the file: write the summary
   sentence, merge bullets that describe one change, order each section.
   The release workflow refuses a stable tag without the notes, and
   `check-versions.mjs --tag` (step 3, and the workflow's preflight) a
   stable tag with fragments left over or the summary unwritten; the notes
   are the GitHub release's text and the updater's.
2. The pull request (`Textus X.Y.Z`) runs the full suite; squash-merge it
   once that passed.
3. Check the merged commit on `main` and tag it:

   ```sh
   git fetch origin
   node scripts/check-versions.mjs --tag vX.Y.Z   # on a checkout of origin/main
   git tag -a vX.Y.Z origin/main -m "Textus X.Y.Z"
   git push origin vX.Y.Z
   ```

   Tag only after the merge has landed: a tag pushed before it points at the
   old commit, and the workflow's preflight stops it.

4. `release.yml` runs, as for every tag (below).

### What the workflow does

1. `release.yml` checks that the tag matches the version files and that
   the tagged `mygo.json` has the key its updates are signed for, and runs
   the full CI suite on the tagged commit (`ci.yml`, every job on every
   platform); the builds start only once it passed. The pull request that
   set the version ran the same suite, so this run should hold no surprise -
   look at it whole, `e2e-webkit` and `linux-app` included. A flaky job is
   rerun alone (`gh run rerun <run> --failed`); a real failure is fixed on
   `main` and the unpublished tag moved to the new commit. Run
   `npm run test:perf` once and compare its numbers with the last
   release's: a reference, not a gate. Then `mygo build` builds the apps
   for macOS, Windows and Linux, x64 and ARM64 each (see
   [Installers](#installers)), on two kinds of runner: `macos-26` signs and
   notarizes the Mac apps and their disk images (a fork without the
   `APPLE_*` secrets signs them ad hoc and skips the notary service; the
   release of `CatVinci-Studio/Textus` stops without them), and `ubuntu-24.04` builds
   the Linux apps and packages and the Windows apps with their NSIS
   installers (MyGo needs no cgo, so neither ARM64 nor Windows needs a
   runner of its own).
   Each build uploads its installers and its signed update (the archive,
   the deltas and `update-<target>.json`) to the tag's draft. Then the
   workflow writes the updater manifest onto the draft, publishes the
   GitHub release, points the beta channel's manifest at it, mirrors the
   installers to the website's download bucket, and sends the tested
   `textus-bin` package to the `[catvinci]` pacman repository in
   `CatVinci-Studio/arch-repo` (see `packaging/arch/README.md`). In a fork
   the workflow ends once the release is published: the beta manifest,
   the mirror, the pacman repository and the playground are
   CatVinci-Studio's, and their jobs skip other repositories.

   The Homebrew cask is not ours to update: it is the official
   [`textus` cask](https://github.com/Homebrew/homebrew-cask/blob/main/Casks/t/textus.rb)
   (`Casks/t/textus.rb` in `Homebrew/homebrew-cask`), and Homebrew's bot
   bumps it after each stable release, usually within a few hours.

2. Verify the release:
   - the GitHub release is public and has every file in
     [Installers](#installers), with an `update-<target>.json` for each of
     the six targets;
   - the release has no `latest.json`, and `textus-latest.json` has the new
     version with the keys listed there (`jq '.platforms | keys'`):
     `mygo-darwin-arm64`, `mygo-darwin-amd64`, `mygo-linux-amd64`,
     `mygo-linux-arm64`, `mygo-windows-amd64` and `mygo-windows-arm64`;
   - <https://textus.md/download/manifest.json> names the new
     files under the ten mirror keys in [Installers](#installers),
     `windows-arm64`, `deb-arm64`, `rpm-arm64` and the two Linux archives'
     included;
   - <https://textus.md/download/textus-beta.json> has the new
     version;
   - the Arch repository has `textus-bin-X.Y.Z-1`;
   - later that day, `brew info --cask textus` shows the new version. If
     Homebrew's bot hasn't bumped it after a day, open the bump pull
     request by hand with `brew bump-cask-pr textus --version X.Y.Z`.

   The first release built with MyGo changes what the Tauri releases
   (up to 1.4.6) had, so check these by hand once:
   - the website's download page and the Homebrew cask find their files
     by name: the disk images are now `textus-X.Y.Z-arm64.dmg` and
     `textus-X.Y.Z-amd64.dmg` (were `Textus_X.Y.Z_aarch64.dmg` and
     `…_x64.dmg`), and there is no AppImage, Android APK or portable zip,
     so the mirror has no `linux`, `linux-arm64`, `android` or `…-portable`
     key; the Linux archives, which MyGo's install script installs, take
     the AppImages' place;
   - installs of the Tauri builds don't move to it by themselves
     ([Updater manifest](#updater-manifest)).

A failed run can be rebuilt with
`gh workflow run release.yml --ref vX.Y.Z -f tag=vX.Y.Z`: the workflows, the
preflight checks and the Arch package's scripts (`packaging/arch/`) come from
the ref; the app, its installers and tarball, and the updater manifest are
built from the tag. The ref is the tag because the **release** environment
admits only tags ([Secrets](#secrets)). The three steps after publishing are
separate workflows, so one of them can be retried alone:
`gh workflow run mirror-downloads.yml --ref vX.Y.Z -f tag=vX.Y.Z`, and the
same for `update-beta-manifest.yml` and `update-arch-repo.yml`.

## Installers

Each release carries these, named by `mygo build` (`X.Y.Z` is the
version; a pre-release's `.deb` and `.rpm` write it `X.Y.Z~rc.N`). The
download mirror's `manifest.json` gives the website one file per mirror
key.

| Platform          | File                                    | Built on               | Mirror key            |
| ----------------- | --------------------------------------- | ---------------------- | --------------------- |
| macOS, Apple      | `textus-X.Y.Z-arm64.dmg`                | `macos-26`             | `mac`                 |
| macOS, Intel      | `textus-X.Y.Z-amd64.dmg`                | `macos-26` (cross)     | `mac-intel`           |
| Windows x64       | `textus-setup-X.Y.Z-amd64.exe`          | `ubuntu-24.04` (cross) | `windows`             |
| Windows ARM64     | `textus-setup-X.Y.Z-arm64.exe`          | `ubuntu-24.04` (cross) | `windows-arm64`       |
| Linux x64         | `textus_X.Y.Z_amd64.deb`                | `ubuntu-24.04`         | `deb`                 |
| Linux x64         | `textus-X.Y.Z-1.x86_64.rpm`             | `ubuntu-24.04`         | `rpm`                 |
| Linux x64         | `textus-X.Y.Z-linux-amd64.tar.gz`       | `ubuntu-24.04`         | `linux-archive`       |
| Linux ARM64       | `textus_X.Y.Z_arm64.deb`                | `ubuntu-24.04` (cross) | `deb-arm64`           |
| Linux ARM64       | `textus-X.Y.Z-1.aarch64.rpm`            | `ubuntu-24.04` (cross) | `rpm-arm64`           |
| Linux ARM64       | `textus-X.Y.Z-linux-arm64.tar.gz`       | `ubuntu-24.04` (cross) | `linux-arm64-archive` |
| Arch Linux x86_64 | `Textus_X.Y.Z_linux_x86_64.tar.gz`      | `ubuntu-24.04`         | -                     |
| Arch Linux x86_64 | `textus-bin-X.Y.Z-1-x86_64.pkg.tar.zst` | Arch container         | -                     |

The release also carries MyGo's install script, which installs the Linux
archive for the user, in `~/.local/textus.app`, where the app can update
itself:

```sh
curl -fsSL https://github.com/CatVinci-Studio/Textus/releases/latest/download/install.sh | sh
```

The `.deb` and `.rpm` install the app in `/opt/textus`, where it can't
([Update channels](#update-channels)). Beside these, each of the six
targets (`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`,
`windows-amd64`, `windows-arm64`) has a signed update: the app as
installed, `textus-X.Y.Z-<target>.tar.gz` (on Linux the archive above),
deltas from the versions before it (`textus-A.B.C-to-X.Y.Z-<target>.delta`,
`updates.deltas` in `mygo.json`), and `update-<target>.json`, which names
them with their sizes and signatures. The updater manifest
(`textus-latest.json`) gathers the six under the keys `mygo-<target>`.

The Arch package stays x86_64: it is built and tested in the x86_64-only
`archlinux` container, and `[catvinci]` has no aarch64 repository. Its
tarball holds a second build, linked as the Arch package's
(`backend/distribution.go`), which reports new versions but leaves
installing them to pacman. `scripts/updater-manifest.mjs` and
`scripts/mirror-keys.mjs` map the file names to the keys; their tests
list every name above, and the Tauri builds' names.

## Pre-releases

A tag with a semver pre-release suffix (`v1.3.10-rc.1`; the 1.3.0 test
builds were `v1.3.0-beta.N`) is published as a GitHub pre-release. The
version files carry the same suffix (`1.3.10-rc.1`):
the tag check compares the whole version, and release notes are only
required for stable tags. The `.deb` and `.rpm` write it `1.3.10~rc.1`,
which both package managers sort before `1.3.10` (an RPM version can't
contain `-`); the Tauri builds made no `.rpm` of a pre-release.

A pre-release reaches only the users who chose **Settings › General ›
About Textus › Update channel › Beta**. It skips the download mirror and
the `[catvinci]` pacman repository, and the stable channel never sees it: that channel reads
`releases/latest/download/textus-latest.json`, and `releases/latest`
excludes pre-releases. To check a pre-release before tagging the
stable release, switch a test install to Beta and use **Check for Updates**.

## Update channels

The app has two (`backend/updater.go`):

- **Stable** reads the newest stable GitHub release's `textus-latest.json`
  (`stableManifestURL`).
- **Beta** reads `textus-beta.json` (`betaManifestURL`), and falls back to
  the stable endpoint when that fails. GitHub has no fixed URL for the
  newest pre-release, so CatVinci-Studio/Textus keeps the beta manifest on
  its own download bucket (<https://textus.md/download/textus-beta.json>).

Both URLs name atoz03/Textus, whose releases are signed with the key in
`mygo.json`; CatVinci-Studio/Textus signs its own with another. No
workflow writes atoz03/Textus's beta manifest yet (the job below writes
the bucket, and skips other repositories), so its beta channel reads
stable.

Either way the app takes its own entry (`mygo-<GOOS>-<GOARCH>`, or
`mygo-darwin-universal` on a Mac), and MyGo's updater downloads that
archive or a delta, checks its size and its Ed25519 signature against
`updates.publicKey` in `mygo.json`, and replaces the app in place.

After every published tag, pre-releases included, the
`update-beta-manifest` job (`update-beta-manifest.yml`,
`scripts/update-beta-manifest.sh`) copies that release's
`textus-latest.json` to `textus-beta.json` in the `textus-downloads` R2
bucket - unless the bucket already holds a newer version, so rebuilding an
old tag never moves beta backwards. Beta therefore always offers the newest
of the latest pre-release and the latest stable release: on
`1.1.0-rc.2`, the stable `1.1.0` arrives as an update. Its download URLs are
the GitHub release's assets; pre-releases are not mirrored. It needs
the `CLOUDFLARE_API_TOKEN` secret that the download mirror uses.

The app never installs an older version than it runs, so a user who goes
back from Beta to Stable keeps the pre-release until a newer stable
version is out. Builds that don't install their own updates (the Arch
package, iOS) have no channel setting and check the stable channel. An
install from the `.deb` or `.rpm` finds updates too, but can't install
them: the app is in `/opt/textus`, which it can't write, so MyGo's updater
is off (`mygo.Updater.Enabled()`) and the app says to download the new
version.

## Updater manifest

Textus's manifest is `textus-latest.json`, never `latest.json`. Levis 0.9.x
installs read `latest.json` from this repository (the old
`CatVinci-Studio/Levis` URL redirects here) and trust the same signing key, so
a `latest.json` on a Textus release would install Textus over Levis.
`mygo build` writes each target's own manifest, `update-<target>.json`,
which the build jobs upload to the draft; before publishing, the publish
job gathers them into `textus-latest.json`
(`scripts/updater-manifest.mjs build`), each under `mygo-<target>` with
its archive's release URL, size, signature and deltas, and refuses to
publish a draft that has a `latest.json` at all (`… guard`). The release
fails rather than leave out a platform whose update is missing.

Releases up to 1.1.0 carried a copy of Levis 0.9.8's own `latest.json`, so
Levis installs updated to that last Levis, which told its user about Textus.
Levis support ended after 1.1.0: later releases carry no copy, and once one
of them is "Latest" a Levis install finds no manifest and stays as it is. The
name stays forbidden for the reason above. A rebuild of a 1.1.0-or-older tag
stops at the guard until that copy is deleted from its release.

The Tauri builds (up to 1.4.6, and the 1.4.7 test builds) read
`textus-latest.json` too, but only their own keys (`darwin-aarch64`,
`linux-x86_64-deb`, …), signed with the Tauri key
(`TAURI_SIGNING_PRIVATE_KEY`), which a Go release doesn't carry: their
check fails, as the manifest has no entry for their platform, and they
stay where they are. Bringing them to the Go builds needs a bridge release that the Tauri
updater accepts; whether to make one is the maintainers' decision.
`scripts/updater-manifest.mjs` still checks a Tauri tag's configuration,
so the Tauri releases can be rebuilt.

`npm run check` checks this checkout as `… check` does (in the script's
tests), and fails if the updater endpoint, the beta channel's endpoint or
the workflow drifts back. On a tag, the release's preflight also requires
`updates.publicKey` in `mygo.json` (the public half of
`MYGO_UPDATER_PRIVATE_KEY`, [Secrets](#secrets)) and `updates.github`:
a build without them can never update itself. The beta manifest is
`textus-beta.json`, which CatVinci-Studio/Textus keeps on its download
bucket, never on a version's GitHub release.

## Secrets

The signing, notarizing, upload and deploy secrets live in the repository's
**release** environment (Settings › Environments › release), not in the
repository's own secrets: `APPLE_*`, `MYGO_UPDATER_PRIVATE_KEY`,
`CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_PLAYGROUND_TOKEN` and
`ARCH_REPO_DEPLOY_KEY`, and for the Tauri builds, up to 1.4.7-rc.4 (a rebuild,
a bridge release, `android-release.yml`) `TAURI_SIGNING_PRIVATE_KEY` and
`ANDROID_*` (the APK's signing key). `MYGO_UPDATER_PRIVATE_KEY` signs the
updates: it is made once with `mygo keygen`, its public half is
`updates.publicKey` in `mygo.json`, and it can't be replaced, as installed
apps accept only updates signed with it. The jobs that use the secrets
(`release.yml` build, `android-release.yml`, `ios-release.yml`,
`mirror-downloads.yml`, `update-beta-manifest.yml`,
`update-arch-repo.yml`, `deploy-playground.yml`) name the environment, and it
admits only `v*` and `ios-v*` tags. So a job run by hand - a retried mirror,
a rebuilt Arch package - is started from the release's tag, not from main:

```sh
gh workflow run mirror-downloads.yml --ref v1.3.2 -f tag=v1.3.2
```

A copy of each secret is kept off GitHub by the maintainer.

## Rebuilds

Running the Release workflow by hand on an existing tag rebuilds and
republishes that release. The download mirror and the pacman repository
only move when the tag is the newest stable one, and the
beta manifest only when the tag is not older than the one it names, so
rebuilding an old tag never downgrades anyone.

## Windows installer

Windows ships only MyGo's NSIS installer (`textus-setup-X.Y.Z-<arch>.exe`),
for stable and pre-release tags alike, made on Ubuntu with Ubuntu's NSIS
from `mygo.json` and `packaging/windows/installer.nsh`. See
[windows-installer.md](windows-installer.md) for what it does, and for
the Tauri builds' installer and MSI it replaces.
