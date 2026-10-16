# Releasing (maintainers)

A release is a version tag. Arrmada is pre-1.0: versions are `v0.x` until the owner has
tested the finished roadmap. From an up-to-date `main`:

```sh
git tag -a v0.18.0 -m "Arrmada 0.18.0"
git push origin v0.18.0
```

Pushing the tag runs the Release workflow (`.github/workflows/release.yml`): it builds the
image stamped with the version and commit, pushes it to `ghcr.io/tristenlammi/arrmada` as
`0.18.0`, `0.18`, `sha-<commit>` and `stable`, and then creates the GitHub release with
generated notes. A tag with a hyphen (`v0.18.0-rc.1`) is published as a pre-release: it gets
its own version tag but never `stable`, and the in-app update check doesn't offer it. Pushes
to `main` and pull requests only build the image as a check (`image.yml`); they never
publish one.

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

Unraid users on the template follow `:stable`, so a release reaches them with nothing else to
do; the Updates card tells them to update from Unraid's Docker tab. Listing the template in
Community Apps, and looking after it, is in [Listing Arrmada in Community Apps](unraid-submission.md).
