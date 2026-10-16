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

// buildChannel is the channel the update check reports, or — where there's no check to
// ask (a manager, a server without it wired) — the same reading of the build that
// updatecheck's channelOf makes, minus the install's own channel, which only it knows: a
// v-tag is a release, a known commit a build of main, anything else dev.
export function buildChannel(version: string, commit: string, update?: UpdateStatus | null): UpdateStatus["channel"] {
  if (update?.channel) return update.channel;
  if (/^v\d+\.\d+\.\d+(?:[-+].*)?$/.test(version)) return "release";
  if (/^[0-9a-f]{7,40}$/.test(commit)) return "main";
  return "dev";
}

// versionBadge is the sidebar's quiet build line: "v0.2.0" for a release, "main · c9d20ec"
// for a build of a commit (both channels that follow a commit compile main), "dev" for a
// build that doesn't know its commit. A build with local edits keeps its version's -dirty
// mark. update says whether the check found something newer for this install's channel
// (the Updates card's headline, so the two never disagree); title is the badge's full name.
export function versionBadge(version: string, commit: string, update?: UpdateStatus | null): { label: string; update: boolean; title: string } {
  const channel = buildChannel(version, commit, update);
  const dirty = version.endsWith("-dirty") ? "-dirty" : "";
  const label = channel === "release" ? version
    : channel === "main" || channel === "build" ? `main · ${commit.slice(0, 7)}${dirty}`
      : "dev";
  const name = `Arrmada ${label}`;
  if (!update?.enabled || !update.checked_at) return { label, update: false, title: name };
  const tone = updateHeadline(update).tone;
  if (tone === "update") {
    const what = update.channel === "main"
      ? `${update.behind} commit${update.behind === 1 ? "" : "s"} behind main`
      : update.latest?.tag ?? "";
    return { label, update: true, title: `${name} — update available${what ? ` (${what})` : ""}` };
  }
  return { label, update: false, title: tone === "ok" ? `${name} — up to date` : name };
}

// The install line: run again on the server, it finds the install and updates it the way
// its channel says (the latest release, or main compiled there).
export const INSTALL_LINE = "curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh";

// The words for a container Unraid's own Docker manager runs (Community Apps or the
// template): Unraid pulls the new image itself, and the install line would set up a second,
// Compose install beside it.
export const UNRAID_UPDATE = "Unraid runs this container: to update, open Unraid's Docker page, click Check for Updates, then apply the update on Arrmada. Arrmada copies its database to its backups folder before it upgrades it.";

// updateHint is how this server gets the update on offer, for the channel it follows (the
// words, and the command or line to use, if any), or null when there's nothing to do.
export function updateHint(u: UpdateStatus): { text: string; command?: string } | null {
  if (!u.enabled) return null;
  if (u.host === "unraid") {
    const offered = u.available || (u.channel === "main" && (u.behind ?? 0) > 0);
    return offered ? { text: UNRAID_UPDATE } : null;
  }
  if (u.channel === "release" && u.available) {
    return {
      text: "To update, run the install line again on the server; it backs up the database first. Installed with just the compose file: docker compose pull && docker compose up -d.",
      command: INSTALL_LINE,
    };
  }
  if (u.channel === "build" && u.available) {
    return {
      text: "Run the install line again on the server to move it to that release; from then on updates download each release.",
      command: INSTALL_LINE,
    };
  }
  if (u.channel === "build" && (u.ahead ?? 0) > 0) {
    return {
      text: "Updates keep this build until a release includes it. To follow main instead, add this line to .env in Arrmada's folder, then run the install line again:",
      command: "ARRMADA_CHANNEL=main",
    };
  }
  if (u.channel === "main" && (u.behind ?? 0) > 0) {
    return { text: "To update, run the install line again on the server: on main it pulls the newest code and compiles it.", command: INSTALL_LINE };
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
