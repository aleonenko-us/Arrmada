# Security

## Reporting a vulnerability

Please **don't** open a public issue for a security problem. Report it privately instead:
on GitHub, open the repository's **Security** tab and choose **Report a vulnerability**. Only
the maintainer sees the report.

Helpful to include: what's affected (sign-in, an API route, file handling, the installer…),
the Arrmada version (shown at the foot of the sidebar), how to reproduce it, and what an
attacker would need (network access, an account, which role).

You'll get an answer as soon as I can manage — this is a one-person project, so give it a few
days — and a fix in a release with credit in its notes, unless you'd rather stay anonymous.

## Supported versions

Arrmada is pre-1.0. Fixes go into the latest release (`stable`) and `main`; older releases
aren't patched, so update to get a fix (`./update.sh`, or pull `:stable`).

## What's in scope

Anything in this repository: the app, its API and web UI, the audiobook server, the install
and update scripts, the Docker images and the Unraid template. Problems in bundled third-party
software (qBittorrent, FlareSolverr, SABnzbd, Prowlarr) belong with those projects, but tell
me if Arrmada's setup of them makes things worse.

## Running it safely

- Keep Arrmada's web UI on your network or behind a reverse proxy with HTTPS. Don't publish
  the bundled qBittorrent, SABnzbd or Prowlarr ports to the internet.
- Turn on two-factor sign-in for admin accounts (Me → Two-factor sign-in).
- Personal API keys can do what their owner can on the routes listed in
  [the API docs](docs/api.md); give scripts read-only keys where you can, and revoke keys
  you no longer use.
- Database backups contain credentials (connection keys, two-factor secrets). Keep them
  private.
