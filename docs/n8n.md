# Alerts to n8n (or any webhook)

Arrmada can send every admin alert to [n8n](https://n8n.io) — or any other URL that takes a
JSON `POST` — so you can build your own automations: post to Discord your way, add a row to a
sheet, run a script, open a ticket. Each alert arrives as structured JSON with the event's
details (ids, titles, years, quality, who asked), not just a line of text.

It's an alert connection like the Discord or ntfy ones: Settings → Alerts → **Add alert** →
**n8n / Webhook**. You pick its events the same way, it retries the same way, and its card
shows the same delivery log.

Only admins can add one, and only for admin alerts. Requesters' own notifications ("your
request is ready") can't go to a webhook (see [Security](#security) for why).

## Set it up

### 1. In n8n: a Webhook node

The quickest start is the example workflow: in n8n, **Workflows → Import from file** and pick
[`examples/n8n-arrmada-alerts.json`](examples/n8n-arrmada-alerts.json). It has the Webhook
node, a signature check, and a Switch that sends each kind of alert down its own branch,
with placeholder steps to replace.

To build your own instead, add a **Webhook** node and set:

- **HTTP Method:** `POST`
- **Path:** anything, e.g. `arrmada`
- **Authentication:** `None`, `Header Auth` or `Basic Auth` (see [Authentication](#authentication))
- **Respond:** `Immediately` (the default). Arrmada only needs a `2xx` answer.

Copy the node's **Production URL**, e.g. `http://192.168.1.10:5678/webhook/arrmada`, and
publish (activate) the workflow. The production URL only answers while the workflow is
published.

An n8n on your own network is fine — that's where most people run it. What matters is that
Arrmada's container can reach it: if both run in Docker, use an address Arrmada can see (the
host's LAN IP, or the n8n container's name on a shared Docker network), not `localhost`.

### 2. In Arrmada: the connection

Settings → Alerts → **Add alert** → **n8n / Webhook**:

- **Webhook URL** — the Production URL.
- **Authentication** — the same as the Webhook node's.
- **Signing secret** — made for you. **Copy it now**: once the connection is saved it's never
  shown again (press **Make a new one** to replace it, then update n8n).
- Tick the events you want, then **Add**.

Press **Send test**. The connection's log says whether n8n took it, and if not, why
(`404` usually means the workflow isn't published).

### 3. Map the fields with "Listen for test event"

n8n's **Listen for test event** listens on the node's *Test* URL (`/webhook-test/…`), not
the production one. To capture a real-looking alert while you build the workflow:

1. In n8n, open the Webhook node and press **Listen for test event**.
2. In Arrmada, on the connection, press **Replace** next to the Webhook URL and paste the
   **Test URL** — don't save.
3. Pick an event under **Send test** (e.g. *Movie imported*) and press **Send test**.
   Arrmada sends that event with realistic made-up values and `"test": true`.
4. n8n shows the payload; map fields from it.
5. Back in Arrmada press **Keep saved** (or reload the page), so the connection keeps the
   Production URL.

## Authentication

The signature (below) already proves an alert came from your Arrmada, so authentication is an
extra lock on the door, not a requirement. If you want it:

- **Header Auth** — in n8n, set the Webhook node's Authentication to *Header Auth* and create a
  credential with a **Name** (e.g. `X-Arrmada-Key`) and a long random **Value**. In Arrmada choose
  *Header Auth* and enter the same name and value. Header names Arrmada sets itself
  (`Content-Type`, `X-Arrmada-Signature` and the other headers listed below) can't be used.
- **Basic Auth** — the same, with a username and password on both sides.

The value and password are secrets: Arrmada never shows them again after saving.

## Check the signature

Every request carries:

| Header | Value |
|---|---|
| `X-Arrmada-Timestamp` | When this attempt was sent, in Unix seconds. |
| `X-Arrmada-Signature` | `sha256=` + hex HMAC-SHA256 of `<timestamp>.<raw body>`, keyed with the signing secret. |
| `X-Arrmada-Event` | The event key, e.g. `movie.imported` (`test` for a plain test). |
| `X-Arrmada-Delivery` | The delivery's id. A retry keeps the same id and the same body. |
| `User-Agent` | `Arrmada/<version>` |

Check that the signature matches **and** that the timestamp is recent (a few minutes): the
timestamp is part of what's signed, so a captured request can't be sent again later with a new
one.

Arrmada writes its JSON so that re-serialising n8n's parsed body (`JSON.stringify($json.body)`)
gives back exactly the bytes it signed — no Raw Body option needed.

### Without code: the example workflow

The example workflow checks it with two built-in nodes, which work on every n8n:

1. **Crypto** node — Action *Hmac*, Type *SHA256*, Encoding *hex*, a Crypto credential whose
   **Hmac Secret** is the signing secret, Property Name `expected_signature`, and Value:

   ```
   {{ $json.headers['x-arrmada-timestamp'] }}.{{ JSON.stringify($json.body) }}
   ```

2. **If** node — both of:
   - `{{ $json.headers['x-arrmada-signature'] }}` *is equal to* `sha256={{ $json.expected_signature }}`
   - `{{ Math.abs($now.toSeconds() - Number($json.headers['x-arrmada-timestamp'])) }}` *is less than or equal to* `300`

   The *false* branch ends in a **Stop and Error** node.

### In a Code node

If you'd rather check it in one **Code** node (JavaScript, *Run Once for All Items*), use the
snippet below. It needs Node's `crypto` module, which self-hosted n8n blocks in the Code node
unless you allow it: set the environment variable `NODE_FUNCTION_ALLOW_BUILTIN=crypto` on n8n
and restart it. (Otherwise use the nodes above.)

<!-- signature-snippet -->
```js
// Checks each alert really came from your Arrmada and isn't an old one sent again.
// Needs NODE_FUNCTION_ALLOW_BUILTIN=crypto on self-hosted n8n.
const SECRET = 'paste-your-signing-secret-here';
const MAX_AGE_SECONDS = 300;

const crypto = require('crypto');
const out = [];
for (const item of $input.all()) {
  const headers = item.json.headers || {};
  const timestamp = String(headers['x-arrmada-timestamp'] || '');
  const signature = String(headers['x-arrmada-signature'] || '');
  // Arrmada writes its JSON so that re-serialising the parsed body gives back the exact bytes it signed.
  const body = JSON.stringify(item.json.body);
  const expected = 'sha256=' + crypto.createHmac('sha256', SECRET).update(timestamp + '.' + body).digest('hex');
  const a = Buffer.from(signature);
  const b = Buffer.from(expected);
  if (!timestamp || a.length !== b.length || !crypto.timingSafeEqual(a, b)) {
    throw new Error('Not from Arrmada: the signature does not match. Check the SECRET line.');
  }
  if (Math.abs(Date.now() / 1000 - Number(timestamp)) > MAX_AGE_SECONDS) {
    throw new Error('This alert is more than 5 minutes old: a replay, or the clocks disagree.');
  }
  // Pass on just the alert itself.
  out.push({ json: item.json.body });
}
return out;
```

Anything else can check it the same way. In a shell, with the body saved to `body.json`:

```sh
printf '%s.%s' "$TIMESTAMP" "$(cat body.json)" | openssl dgst -sha256 -hmac "$SECRET"
```

## The payload

```json
{
  "version": 1,
  "event": "movie.imported",
  "event_label": "Movie imported",
  "group": "library",
  "severity": "info",
  "occurred_at": "2026-10-16T09:30:00Z",
  "test": false,
  "title": "Imported",
  "message": "📥 Dune (upgraded)",
  "url": "https://arrmada.example.com/movies/12",
  "path": "/movies/12",
  "image_url": "https://image.tmdb.org/t/p/w500/…jpg",
  "data": { "movie_id": 12, "tmdb_id": 438631, "title": "Dune", "year": 2021, "…": "…" },
  "instance": { "name": "Arrmada", "version": "0.4.0", "url": "https://arrmada.example.com" }
}
```

| Field | Meaning |
|---|---|
| `version` | The payload's version, `1`. It only goes up when a field is removed or changes meaning; new fields can appear without it. |
| `event` | The event's key (the headings below), or `test` for a plain test. |
| `event_label` | The event's name as Settings → Alerts shows it. |
| `group` | `needs_you`, `requests`, `downloads`, `library`, `plex` (streams on Plex or Jellyfin) or `convert`. |
| `severity` | `info`, `warning` or `error`. |
| `occurred_at` | When it happened (RFC 3339, UTC). A retry sends the same time. |
| `test` | `true` for **Send test**: the values in `data` are made up. |
| `title`, `message` | The same words a chat alert gets. Mentions such as `@everyone` are defused. |
| `url` | A link that opens it in Arrmada; `null` until Settings → System → **Outside address** is set. |
| `path` | The same link inside Arrmada (`/movies/12`), or `null`. |
| `image_url` | The poster, for library alerts that have one (TMDB / Open Library), else `null`. |
| `data` | The event's own fields — see below. Every field listed is always there; one the event doesn't know is `null`. |
| `instance` | Which Arrmada sent it: `name`, `version` and its outside address (`url`, or `null`). |

The values in `data` are raw — a title is exactly the title. If you post them to a chat that
reads mentions or markdown, escape them there.

## Delivery

- An alert is queued, then sent: up to three connections at a time, one at a time per
  connection, so a slow n8n holds up only its own alerts.
- Any `2xx` answer counts as delivered. Anything else, or no answer within 15 seconds, is
  retried after 1, 5 and 30 minutes, then marked failed in the connection's log.
- Delivery is *at least once*: after a crash an alert can arrive twice. Each retry carries the
  same `X-Arrmada-Delivery` id and body, so use that to drop repeats if it matters.
- Redirects aren't followed (the auth header would go with them): use the final URL.

## Security

- **Where it may point.** Anywhere: an address on your network included. This is an
  admin-only connection, and admins' Apprise connections can already post JSON to any host;
  n8n almost always runs on the LAN, so refusing private addresses would refuse the very
  thing it's for. Requesters can't create webhooks: their personal notification links are
  held to public push services, and that rule isn't loosened for webhooks.
- **Secrets.** The URL (n8n's webhook path works like a password), the auth value or password
  and the signing secret are write-only: once saved they never come back from the API, the
  page shows "•••• saved", errors and logs never quote them, and the support bundle blanks
  them.
- **What's sent.** Only the fields listed below — nothing a future version adds to an
  internal event reaches a webhook until it's added here. People appear by display name only;
  no email addresses, account ids, tokens, API keys or download links are ever sent. Nothing
  about what anyone listens to is ever sent: there's no event for it.

## Events

Every event's `data` fields. `null` means the event doesn't know that value.

<!-- BEGIN EVENT REFERENCE: generated by `go test ./internal/notify -run TestWebhookDocsUpToDate -update` -->

### Needs you

#### `import.held` — Import held for review

A finished download needs a decision before it can be imported. Severity: warning (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `import.stuck` — Import keeps failing

A finished download keeps failing to import, or finished in a category Arrmada doesn't import. Severity: warning (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `download.failed` — Download failed

The download client stopped a download with an error. Severity: error (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `download.stalled` — Download stalled

Nobody has sent a download any data for an hour. Severity: warning (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `health.problem` — Health problem

A health check found something wrong: a client or Plex unreachable, downloads paused for disk space, a folder missing. Severity: warning (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `health.resolved` — Health problem resolved

A health problem you were told about has cleared. Severity: info (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

### Requests

#### `request.created` — New request

Someone asked for something and it's waiting for your approval. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `request_id` | integer | The request's id. |
| `media_type` | string | movie, series or book. |
| `tmdb_id` | integer | TMDB id (null for a book). |
| `title` | string | The title asked for. |
| `year` | integer | Release year. |
| `seasons` | string | For a show: which seasons (e.g. "S1–2"); empty for the whole show. |
| `requested_by` | string | The requester's display name. |
| `note` | string | The requester's note, if they wrote one. |
| `rerequest` | boolean | true when it was declined before and asked for again. |
| `decline_reason` | string | Why it was declined before (re-requests). |

#### `request.auto_approved` — Auto-approved request

Someone allowed to auto-approve asked for something, and it's on its way. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `request_id` | integer | The request's id. |
| `media_type` | string | movie, series or book. |
| `tmdb_id` | integer | TMDB id (null for a book). |
| `title` | string | The title asked for. |
| `year` | integer | Release year. |
| `requested_by` | string | The requester's display name. |

#### `issue.reported` — Problem reported

Someone reported a problem with a movie or episode in the library. Severity: warning.

| Field | Type | Meaning |
|---|---|---|
| `issue_id` | integer | The issue's id. |
| `media_type` | string | movie or series. |
| `tmdb_id` | integer | TMDB id. |
| `title` | string | The title the problem is with. |
| `year` | integer | Release year. |
| `season` | integer | For an episode: its season. |
| `episode` | integer | For an episode: its number. |
| `category` | string | video, audio, subtitles, wrong_file or other. |
| `note` | string | What the reporter wrote. |
| `reported_by` | string | The reporter's display name. |

### Downloads

#### `release.grabbed` — Grabbed

A release was sent to the download client. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `release` | string | The release's name as the indexer listed it. |
| `indexer` | string | The indexer it came from ("manual" for an uploaded file). |
| `network` | string | torrent or usenet. |
| `media_type` | string | movie, series, book or music (null when it can't be told). |
| `resolution` | string | 2160p, 1080p, 720p, 576p or 480p, read from the release name; null when it doesn't say. |
| `source` | string | Remux, BluRay, WEB-DL, WEBRip, HDTV, DVD or CAM, read from the release name. |
| `codec` | string | x265, x264, AV1, VC-1 or XviD, read from the release name. |
| `hdr` | string[] | HDR formats named in the release name (DV, HDR10, HDR10+, HLG); empty for SDR. |

### Library

#### `movie.imported` — Movie imported

A movie file landed in the library. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `movie_id` | integer | The movie's id in Arrmada. |
| `tmdb_id` | integer | TMDB id. |
| `title` | string | The movie's title. |
| `year` | integer | Release year. |
| `version_id` | integer | The extra version (e.g. a Director's Cut) the file belongs to; null for the main file. |
| `upgrade` | boolean | true when it replaced a file of lower quality. |
| `file` | string | The imported file's full path inside Arrmada's container. |
| `resolution` | string | 2160p, 1080p, 720p, 576p or 480p, read from the file name; null when it doesn't say. |
| `source` | string | Remux, BluRay, WEB-DL, WEBRip, HDTV, DVD or CAM, read from the file name. |
| `codec` | string | x265, x264, AV1, VC-1 or XviD, read from the file name. |
| `hdr` | string[] | HDR formats named in the file name (DV, HDR10, HDR10+, HLG); empty for SDR. |

#### `episodes.imported` — Episodes imported

New episodes of a show landed in the library. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `series_id` | integer | The show's id in Arrmada. |
| `tmdb_id` | integer | TMDB id. |
| `title` | string | The show's title. |
| `year` | integer | The show's first-air year. |
| `count` | integer | How many episodes were imported. |
| `episodes` | string[] | The episodes imported, as S01E02. |
| `release` | string | The download they came from. |
| `size_bytes` | integer | The download's size in bytes. |
| `resolution` | string | 2160p, 1080p, 720p, 576p or 480p, read from the release name; null when it doesn't say. |
| `source` | string | Remux, BluRay, WEB-DL, WEBRip, HDTV, DVD or CAM, read from the release name. |
| `codec` | string | x265, x264, AV1, VC-1 or XviD, read from the release name. |
| `hdr` | string[] | HDR formats named in the release name (DV, HDR10, HDR10+, HLG); empty for SDR. |

#### `book.imported` — Book imported

An ebook or audiobook landed in the library. Severity: info. Only while the books module is on.

| Field | Type | Meaning |
|---|---|---|
| `book_id` | integer | The book's id in Arrmada. |
| `title` | string | The book's title. |
| `author` | string | The author. |
| `year` | integer | Publication year. |
| `edition` | string | ebook or audiobook. |
| `version` | string | For an extra audiobook version: its label (e.g. a narrator). |
| `format` | string | The file format (EPUB, M4B, MP3, …). |
| `size_bytes` | integer | Size of the imported files in bytes. |

#### `music.imported` — Album imported

Tracks of an album landed in the library. Severity: info. Only while the music module is on.

| Field | Type | Meaning |
|---|---|---|
| `artist_id` | integer | The artist's id in Arrmada. |
| `album_id` | integer | The album's id in Arrmada. |
| `artist` | string | The artist. |
| `album` | string | The album. |
| `tracks` | integer | How many tracks were imported. |
| `quality` | string | The tracks' quality (e.g. FLAC, MP3 320). |

#### `book.author_release` — New books by followed authors

A followed author has a new book, and it was added to the library. Severity: info. Only while the books module is on.

| Field | Type | Meaning |
|---|---|---|
| `author_id` | integer | The author's id in Arrmada. |
| `author` | string | The author. |
| `count` | integer | How many new books were added. |
| `titles` | string[] | The new books' titles. |

### Watching

#### `plex.stream.started` — Stream started

Someone started playing something on Plex or Jellyfin. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `server` | string | plex or jellyfin. |
| `user` | string | The viewer's display name on Plex or Jellyfin. |
| `title` | string | What's playing: a film's title, or "Show · Episode" for an episode. |
| `show_title` | string | The show, for an episode. |
| `media_type` | string | movie, episode or track. |
| `year` | integer | Release year. |
| `player` | string | The player's name (e.g. Living Room TV). |
| `platform` | string | The player's platform (e.g. Roku, Chrome). |
| `decision` | string | direct_play, direct_stream or transcode. |
| `location` | string | lan (inside the network) or wan (remote). |
| `item_id` | string | The server's id for the item (Plex's rating key). |

#### `plex.buffering` — Buffering

A Plex stream started buffering. Severity: warning.

| Field | Type | Meaning |
|---|---|---|
| `server` | string | plex or jellyfin. |
| `user` | string | The viewer's display name on Plex or Jellyfin. |
| `title` | string | What's playing: a film's title, or "Show · Episode" for an episode. |
| `show_title` | string | The show, for an episode. |
| `media_type` | string | movie, episode or track. |
| `year` | integer | Release year. |
| `player` | string | The player's name (e.g. Living Room TV). |
| `platform` | string | The player's platform (e.g. Roku, Chrome). |
| `decision` | string | direct_play, direct_stream or transcode. |
| `location` | string | lan (inside the network) or wan (remote). |
| `item_id` | string | The server's id for the item (Plex's rating key). |
| `cause` | string | Why it's buffering, when the server says (e.g. transcoder too slow, network). |

### Convert

#### `convert.failed` — Convert gave up on a file

A file failed to convert three times and is left as it was until you try it again. Severity: warning.

| Field | Type | Meaning |
|---|---|---|
| `title` | string | The file's title (with the version or episode). |
| `item` | string | Convert's key for the file, e.g. movie:12 or episode:7:2:1. |
| `tries` | integer | How many times it failed. |
| `error` | string | The last failure. |

#### `convert.stalled` — Convert is stuck

Conversions are waiting for disk space: the scratch folder, the library disk or the originals' bin is full. Severity: warning (or the item's level).

| Field | Type | Meaning |
|---|---|---|
| `count` | integer | How many items this message is about. 1: title and detail describe it; more: names lists them. |
| `title` | string | The item, in a sentence (null when count is more than 1). |
| `detail` | string | More about the item (null when count is more than 1). |
| `names` | string[] | The items' short names, when count is more than 1. |
| `level` | string | warning or error. |

#### `convert.summary` — Daily Convert summary

One message a morning: how many files were converted, the space freed, and any problems. Severity: info.

| Field | Type | Meaning |
|---|---|---|
| `files` | integer | Files converted since the last summary. |
| `saved_bytes` | integer | Space freed, in bytes. |
| `problems` | integer | Files that ran into a problem. |
| `since` | time | When the counts started. |

<!-- END EVENT REFERENCE -->
