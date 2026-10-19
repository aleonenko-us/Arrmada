# Releasing (maintainers)

A release is a version tag. Arrmada is pre-1.0: versions are `v0.x` until the owner has
tested the finished roadmap.

1. **Write the release notes.** In `CHANGELOG.md`, rename `## [Unreleased]` to the new
   version with today's date, `## [0.18.0] - 2026-10-20` (no `v`), and put a fresh, empty
   `## [Unreleased]` above it. Check the section covers everything since the last release
   (`git log v0.17.0..main --merges`), and that **Before you update** lists every change in
   behaviour and anything people must do by hand (new `.env` lines, a reverse proxy change,
   whether rolling back needs `--with-db`). At the bottom, point `[Unreleased]` at
   `compare/v0.18.0...HEAD` and add a `[0.18.0]` compare link. To see exactly what the
   release will say, run `scripts/changelog-section.sh v0.18.0`. Commit this to `main`.
2. **Wait for the Image workflow** (`image.yml`) on `main` to go green on both platforms
   (Docker build and Docker build (arm64)): that's what proves the commit builds, and it
   warms the caches the release reads.
3. **Tag it**, from an up-to-date `main`:

```sh
git tag -a v0.18.0 -m "Arrmada 0.18.0"
git push origin v0.18.0
```

Pushing the tag runs the Release workflow (`.github/workflows/release.yml`): it builds the
image stamped with the version and commit for `linux/amd64` and `linux/arm64` (each on a
runner of its own platform, the arm64 one on GitHub's free arm64 runner), pushes both to
`ghcr.io/tristenlammi/arrmada` by digest, joins them into one multi-platform image tagged
`0.18.0`, `0.18`, `sha-<commit>` and `stable`, checks both platforms are behind the tag, and
then creates the GitHub release with that version's `CHANGELOG.md` section as its notes,
followed by GitHub's compare link to the previous release. If either platform fails to build,
nothing is tagged and no release is written: open the failed run and use **Re-run all
jobs**, so both platforms are built again from the same tag. A tag with a hyphen
(`v0.18.0-rc.1`) is published as a pre-release: it gets
its own version tag but never `stable`, and the in-app update check doesn't offer it. Pushes
to `main` and pull requests only build the image as a check (`image.yml`, both platforms);
they never publish one.

### If the release notes are missing

With no `## [0.18.0]` section in `CHANGELOG.md`, the **GitHub release** job fails on
purpose: nothing is published with bare notes, because people decide whether to update from
**Before you update**. The images are already out by then (`stable` included, which is what
`update.sh` pulls), but without a release the in-app "Update available" stays quiet. To
finish it, add the section to `CHANGELOG.md` on `main`, then open the run and use **Re-run
failed jobs**. The job reads the tag's `CHANGELOG.md` first and `main`'s second, so the tag
doesn't have to move. Writing the release by hand on GitHub works too, because the job
leaves an existing release alone.

A pre-release (`v0.18.0-rc.1`) needs its own section, `## [0.18.0-rc.1] - <date>`.

## The first release

1. Tag and push as above, and wait for the Release workflow to finish (a cold build takes
   up to a couple of hours).
2. The new package starts **private**. Open it from the repository's Packages list
   (github.com/tristenlammi?tab=packages → arrmada → Package settings), and under
   *Danger zone* change the visibility to **Public**. Until then `docker pull` is denied and
   the installer falls back to compiling on the server.
3. Check it: `docker pull ghcr.io/tristenlammi/arrmada:stable` on any machine, signed out.

From then on, new installs download the image. Every install on the stable channel (the
default) moves to a new release on its next update, and one that compiled `main` moves to the
first release that includes the code it's running ([Updating](updating.md#stable-or-main)).
Only an install with `ARRMADA_CHANNEL=main` in `.env` keeps compiling `main`.

Never put a token in the repository or in `.env`. If the package must stay private, each
server signs in once with `docker login ghcr.io` and a token of its own that can only read
packages.

## The update check

Settings → Status shows when a newer release is out, with its notes (an install on `main`
shows how many commits behind `main` it is instead, and a stable install still running a
build of `main` says whether the latest release includes it yet). It reads GitHub's public list of releases every
12 hours and sends nothing about the server beyond Arrmada's version (in its User-Agent); switching it off on the same card stops every
request.

## Unraid Community Apps

The same run also publishes the Unraid all-in-one image (the Dockerfile's `aio` target:
Arrmada with qBittorrent and FlareSolverr inside), built on top of the plain image it just
pushed, by digest, and tagged `0.18.0-aio`, `0.18-aio`, `sha-<commit>-aio` and `stable-aio`
once the plain tags exist. It never holds the plain release back: if it fails, the release
and its notes are still published, the run shows red, and the `-aio` tags stay where they
were. Fix the cause, then open the run and use **Re-run failed jobs**, which runs the
all-in-one jobs again on the plain image already pushed. Not **Re-run all jobs**: that
builds and publishes the plain tags again too. arm64's all-in-one image has qBittorrent
only (FlareSolverr's Chromium is x86-64 only); Unraid itself is x86-64.

**The template runs `:stable-aio`.** The Community Apps template on `main` is live (Unraid
fetches it from its `TemplateURL`), so it only ever names a tag a release has already
published: it moved to `:stable-aio` after the first release that built the all-in-one
image. If the all-in-one jobs ever stop publishing, fix them before changing anything the
template points at.

Unraid users on the template follow its tag, so a release reaches them with nothing else
to do; the Updates card tells them to update from Unraid's Docker tab. Listing the template in
Community Apps, and looking after it, is in [Listing Arrmada in Community Apps](unraid-submission.md).
