import type { UpdateStatus } from "./api";

// How Settings → Status words the update check, and how it shows release notes.

export type UpdateTone = "update" | "ok" | "muted";

// updateHeadline is the one line the Updates card leads with, and how loudly to say it.
export function updateHeadline(u: UpdateStatus): { text: string; tone: UpdateTone } {
  if (!u.enabled) return { text: "The update check is off.", tone: "muted" };
  if (!u.checked_at) {
    return { text: u.problem ? "Couldn't check for updates yet." : "Not checked yet.", tone: "muted" };
  }
  if (u.channel === "release") {
    if (u.available && u.latest) return { text: `Update available: ${u.latest.tag}`, tone: "update" };
    if (!u.latest) return { text: "No release has been published yet.", tone: "muted" };
    return { text: `Up to date: ${u.current} is the newest release.`, tone: "ok" };
  }
  if (u.channel === "main") {
    if (u.behind === null) return { text: "This build's commit isn't on GitHub, so it can't be compared with main.", tone: "muted" };
    if (u.behind === 0) return { text: "Up to date with main.", tone: "ok" };
    return { text: `${u.behind} commit${u.behind === 1 ? "" : "s"} behind main.`, tone: "update" };
  }
  if (u.channel === "build") {
    if (!u.latest) return { text: "No release has been published yet.", tone: "muted" };
    if (u.available) return { text: `Update available: ${u.latest.tag}`, tone: "update" };
    const ahead = u.ahead ?? null;
    if (ahead === null) return { text: "This build's commit isn't on GitHub, so it can't be compared with the latest release.", tone: "muted" };
    if (ahead > 0) return { text: `This build is newer than the latest release (${u.latest.tag}).`, tone: "ok" };
    return { text: `Up to date: this build is ${u.latest.tag}.`, tone: "ok" };
  }
  return { text: "This build doesn't say which commit it's from, so it can't be compared.", tone: "muted" };
}

// updateHint is how this server gets the update on offer, for the channel it follows, or
// null when there's nothing to do. update.sh is in Arrmada's folder (a git checkout or the
// installer's folder); an install from the compose file alone updates through compose.
export function updateHint(u: UpdateStatus): string | null {
  if (!u.enabled) return null;
  if (u.channel === "release" && u.available) {
    return "To update, run ./update.sh in Arrmada's folder, or the install line again. Installed with just the compose file: docker compose pull && docker compose up -d.";
  }
  if (u.channel === "build" && u.available) {
    return "./update.sh in Arrmada's folder moves this server to that release; from then on updates download each release.";
  }
  if (u.channel === "build" && (u.ahead ?? 0) > 0) {
    return "./update.sh keeps this build until a release includes it. To follow main instead, add ARRMADA_CHANNEL=main to .env and run ./update.sh.";
  }
  if (u.channel === "main" && (u.behind ?? 0) > 0) {
    return "To update, run ./update.sh in Arrmada's folder: it pulls main and compiles it.";
  }
  return null;
}

// A block of release notes: a heading, a bullet or a plain line. Notes are Markdown from
// GitHub; they're shown as text (React escapes it), with just enough shape to read.
export type NoteBlock = { kind: "heading" | "bullet" | "text"; text: string };

// noteBlocks splits notes into lines worth showing: "## What's Changed" is a heading,
// "* x" / "- x" a bullet, blank lines go. Bold/code markers are dropped, nothing else.
export function noteBlocks(notes: string): NoteBlock[] {
  const out: NoteBlock[] = [];
  for (const raw of notes.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const plain = (s: string) => s.replace(/\*\*|__|`/g, "").trim();
    const h = /^#{1,6}\s+(.*)$/.exec(line);
    if (h) { out.push({ kind: "heading", text: plain(h[1]) }); continue; }
    const b = /^[*-]\s+(.*)$/.exec(line);
    if (b) { out.push({ kind: "bullet", text: plain(b[1]) }); continue; }
    out.push({ kind: "text", text: plain(line) });
  }
  return out;
}
