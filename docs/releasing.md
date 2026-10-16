# Releasing (maintainers)

A release is a version tag. Arrmada is pre-1.0: versions are `v0.x` until the owner has
tested the finished roadmap. First wait for the Image workflow (`image.yml`) on `main` to go
green on both platforms (Docker build and Docker build (arm64)): that's what proves the
commit builds, and it warms the caches the release reads. Then, from an up-to-date `main`:

```sh
git tag -a v0.18.0 -m "Arrmada 0.18.0"
git push origin v0.18.0
```

Pushing the tag runs the Release workflow (`.github/workflows/release.yml`): it builds the
image stamped with the version and commit for `linux/amd64` and `linux/arm64` (each on a
runner of its own platform, the arm64 one on GitHub's free arm64 runner), pushes both to
`ghcr.io/tristenlammi/arrmada` by digest, joins them into one multi-platform image tagged
`0.18.0`, `0.18`, `sha-<commit>` and `stable`, checks both platforms are behind the tag, and
then creates the GitHub release with generated notes. If either platform fails to build,
nothing is tagged and no release is written: open the failed run and use **Re-run all
jobs**, so both platforms are built again from the same tag. A tag with a hyphen
(`v0.18.0-rc.1`) is published as a pre-release: it gets
its own version tag but never `stable`, and the in-app update check doesn't offer it. Pushes
to `main` and pull requests only build the image as a check (`image.yml`, both platforms);
they never publish one.

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

**Ordering: the template follows the first `stable-aio`.** The Community Apps template on
`main` is live (Unraid fetches it from its `TemplateURL`) and runs `:stable`, the app on its
own, until a release has published `stable-aio`. Switching it to `:stable-aio` (with the
torrent ports and the built-in switches) is the `feat/unraid-template-aio` branch: merge it
right after that first release's all-in-one jobs are green and `docker pull
ghcr.io/tristenlammi/arrmada:stable-aio` works signed out, never before, or new installs
would point at an image that isn't there.

Unraid users on the template follow its tag, so a release reaches them with nothing else
to do; the Updates card tells them to update from Unraid's Docker tab. Listing the template in
Community Apps, and looking after it, is in [Listing Arrmada in Community Apps](unraid-submission.md).
