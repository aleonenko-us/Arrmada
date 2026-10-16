# Listing Arrmada in Unraid's Community Apps

For maintainers. How the template in this repository gets into Unraid's **Apps** tab, and how
to look after it afterwards. Checked against Unraid's own pages in October 2026; the process
changed in June 2026, so older forum guides (messaging a moderator, a form) no longer apply.

## What's already in the repository

| File | What it's for |
|---|---|
| [`templates/arrmada.xml`](../templates/arrmada.xml) | The template: one container, `ghcr.io/tristenlammi/arrmada:stable`. `templates/template_test.go` keeps it to the rules below |
| [`ca_profile.xml`](../ca_profile.xml) | The maintainer profile Community Apps requires at the repository root (a non-empty `<Profile>`) |
| [`LICENSE`](../LICENSE) | MIT, at the root. Community Apps requires an OSI-approved licence there |
| [`web/public/icon-512.png`](../web/public/icon-512.png) | The icon, 512 × 512 PNG, linked by its raw GitHub address |
| [`docs/unraid.md`](unraid.md) | The user guide the template links to |

**One repository is enough.** Community Apps reads whole GitHub repositories and finds the
template XML files in them, subfolders included; other apps are listed straight from their
source repositories. A separate templates repository isn't needed.

One thing to watch for: the scan reads every `.xml` file in the repository, and two test
files (`internal/indexer/testdata/caps_*.xml`) aren't templates. Older feeds listed such files
as "not an unRaid application" and carried on. If the scan refuses to go further because of
them, make a small repository instead (say `tristenlammi/arrmada-unraid`) holding
`ca_profile.xml`, `LICENSE` and `templates/arrmada.xml`, change the template's `TemplateURL`
to the new repository's raw address, and submit that one.

The rules the template follows, and why:

- **No keys or passwords.** TMDB, Plex, indexers and download clients are set up in the app.
- **No `TZ` entry.** Unraid gives every container it creates the server's timezone (and
  `HOST_OS=Unraid`); a template `TZ` would override it.
- **No `/dev/dri` Device entry.** Unraid passes a blank entry's default, and Docker won't start
  a container whose device doesn't exist. GPU users add `--device=/dev/dri` themselves
  ([Unraid → GPU](unraid.md#gpu-intel-or-amd)).
- **Web UI on host port 7880**, so it doesn't collide with Radarr's 7878. The app still
  listens on 7878 inside, which is what `[PORT:7878]` in the WebUI address refers to.
- **The starting folders are on the media share** (`/storage`), so nothing can land in the
  image's own `/media`, which on Unraid sits inside `docker.img`.

## Step 1: Merge, and try it on your own server

1. Merge the template work into `main`. The template's own address, its icon and the docs all
   point at `main`.
2. Check a stable release is published: `ghcr.io/tristenlammi/arrmada:stable` must exist
   (v0.2.0 does), and the package must stay public on GitHub.
3. Install it by hand on your Unraid server
   ([Install before it's listed](unraid.md#install-before-its-listed)), as a second app next
   to your Compose install. Give it a spare appdata folder (for example
   `/mnt/user/appdata/arrmada-catest`), a free port, and a test share or an empty folder for
   **Media and downloads**. It must not share appdata with your real install. Check it starts,
   the WebUI button opens it, and setup sees `/storage`. Then remove it.

## Step 2: A support thread on the Unraid forum

Community Apps recommends a support link, and its moderators expect maintainers to answer
in a forum thread. Users look for one there.

1. Sign in at [forums.unraid.net](https://forums.unraid.net) with an Unraid account (the same
   account you'll use to submit). Create one at unraid.net if you don't have one.
2. Go to **Application Support → Docker Containers** and start a new topic.
3. Title: `[Support] Arrmada`
4. Post this (edit it as you like):

   > **Arrmada** is one self-hosted app for movies, series, books and audiobooks. It finds,
   > downloads and organises them, takes requests from your household, and adds subtitles,
   > Convert (smaller files) and Plex or Jellyfin watch insights. It replaces Radarr, Sonarr,
   > Readarr, Overseerr and Tautulli in one container.
   >
   > **Template:** this thread's app in Community Apps runs Arrmada on its own and uses the
   > qBittorrent or SABnzbd you already run.
   > **Image:** `ghcr.io/tristenlammi/arrmada:stable` (the latest release, x86-64)
   > **Guide:** https://github.com/tristenlammi/Arrmada/blob/main/docs/unraid.md
   > **Source and bug reports:** https://github.com/tristenlammi/Arrmada
   >
   > **Before you install:** put your libraries and downloads in one share (for example
   > `/mnt/user/data`) and give it to Arrmada and your download client at the same path,
   > `/storage`, so imports are hardlinks rather than copies. Never map media to `/data`;
   > that's Arrmada's database.
   >
   > **Updating:** Docker tab → Check for Updates. Arrmada copies its database before every
   > upgrade and backs it up nightly.
   >
   > **When asking for help,** please include your Arrmada version (bottom of the sidebar),
   > the container's settings, and the support bundle from the Logs page if it's about an
   > error.

5. Post it, and copy the thread's address (for example
   `https://forums.unraid.net/topic/123456-support-arrmada/`).
6. Put the address in the repository, then commit and push to `main`:
   - `templates/arrmada.xml`: replace the GitHub issues link in `<Support>` with it.
   - `ca_profile.xml`: uncomment `<Forum>` and put it there.

## Step 3: Submit

1. Open [ca.unraid.net/submit](https://ca.unraid.net/submit) and sign in with your Unraid
   account.
2. **Add Repository:** `https://github.com/tristenlammi/Arrmada`
3. Run **Validate**, then **Scan**. The scan reads the template and `ca_profile.xml`, checks
   for duplicate listings, and shows a preview of the listing. Check the preview: name, icon,
   categories (Downloaders, Media Apps → Video and Books), the description and the WebUI.
4. Fix anything it reports, push to `main`, and run Validate and Scan again.
5. **Submit.**

Questions about the process go to the Community Apps board on Unraid's product site, linked
from the submit pages.

## What to expect

- **A review.** Every app is approved before it's listed. Moderators turn down apps that copy
  an existing listing or add nothing new in a crowded category (Radarr and friends are
  named as examples), so the support post's "one app instead of five" matters. Closed-source
  apps are generally refused; Arrmada is open source under MIT.
- **No stated turnaround.** The old form said up to 48 hours. Once approved, the app appears
  the next time the feed is rebuilt, which happens several times a day.
- **No download count.** Community Apps counts downloads from Docker Hub only; the image is on
  GitHub's registry, so the count stays empty. Updates work the same either way: Unraid
  checks `ghcr.io` for a new `:stable` like any registry.

## Afterwards

**New releases need nothing here.** Users run `:stable`; publishing a release moves that tag,
and Unraid shows "update ready". Arrmada's own Updates card tells Unraid users to update from
the Docker tab (it recognises a container Unraid made by `HOST_OS=Unraid`).

**Changing the template.** Edit `templates/arrmada.xml` on `main`, keep
`go test ./templates/` passing, and run Validate and Scan on ca.unraid.net again. The feed
picks the change up within hours. Unraid doesn't change templates people have already
installed, so a new or changed field reaches new installs only: if existing users have to
change something, say so in the support thread.

**Answer the support thread.** Moderators expect it, and they can deprecate or remove an app
whose maintainer has gone quiet.

**Moving or renaming the repository** can get the listing removed. Tell the moderators first.

**Retiring the listing.** Add `<Deprecated>true</Deprecated>` to the template (new installs
stop; people who have it keep it) and tell the moderators through the Community Apps board.

**Later, maybe: the whole stack.** The Community Apps format now also accepts Compose
packages (`<AppTemplateType>compose</AppTemplateType>` with a pinned commit of a
`compose.yaml`), which could one day list the bundled qBittorrent and FlareSolverr setup
too. Its documentation was still incomplete in October 2026, so this template is the app
on its own.

## Sources

- Unraid, "New Community Apps" (the June 2026 change): https://unraid.net/blog/new-community-apps
- The submit pages and their help: https://ca.unraid.net/submit,
  https://ca.unraid.net/submit/help,
  https://ca.unraid.net/submit/help/repository-info-xml (`ca_profile.xml`),
  https://ca.unraid.net/submit/help/xml-field-reference (template tags)
- Unraid's starter repository: https://github.com/unraid/unraid-community-apps-starter
- Community Apps policies (approval, duplicates, download counts):
  https://forums.unraid.net/topic/87144-ca-application-policies-privacy-policy/
- Unraid docs, Community Apps (maintainers answer in their threads):
  https://docs.unraid.net/community-applications/
- The forum's Docker Containers board: https://forums.unraid.net/forum/4-application-support/
- Source repositories already in the feed:
  https://raw.githubusercontent.com/Squidly271/AppFeed/master/repositoryList.json
- The feed reading subfolders and flagging non-template XML (2021):
  https://forums.unraid.net/topic/102975-template-errors/
- How Unraid turns a template into `docker run` (TZ, HOST_OS, blank values fall back to
  the default, devices):
  https://github.com/unraid/webgui/blob/master/emhttp/plugins/dynamix.docker.manager/include/Helpers.php
- Template field values the Docker page accepts (Display, Type, Mode, categories):
  https://github.com/unraid/webgui/blob/master/emhttp/plugins/dynamix.docker.manager/include/CreateDocker.php
- Unraid 6.10 release notes (installed templates aren't updated; template repositories
  removed): https://docs.unraid.net/unraid-os/release-notes/6.10.0
