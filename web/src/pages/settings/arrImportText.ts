import type { ArrCounts, ArrNote, ArrResult } from "../../lib/importApi";

// Plain words for the Radarr / Sonarr import card, shared with its tests.

const n = (v: number) => v.toLocaleString("en-US");

function count(v: number, one: string, many: string): string {
  return `${n(v)} ${v === 1 ? one : many}`;
}

// arrCountsLine says what an import would do: "4 films: 1 already in Arrmada, 3 to add
// (2 with their files), 1 can't be matched. 1 wanted film will be searched for a few at a
// time."
export function arrCountsLine(media: "movie" | "series", c: ArrCounts): string {
  const [one, many] = media === "series" ? ["show", "shows"] : ["film", "films"];
  const parts = [`${n(c.existing)} already in Arrmada`, `${n(c.new)} to add`];
  if (c.with_files > 0) parts[1] += ` (${n(c.files_found)} with their ${media === "series" ? "episode files" : "files"})`;
  if (c.unmatchable > 0) parts.push(`${n(c.unmatchable)} can't be matched`);
  let line = `${count(c.titles, one, many)}: ${parts.join(", ")}.`;
  if (c.attachable > 0) line += ` ${count(c.attachable, one, many)} already here will get the file Arrmada was missing.`;
  if (c.wanted > 0) line += ` ${count(c.wanted, `wanted ${one}`, `wanted ${many}`)} will be searched for a few at a time, never all at once.`;
  return line;
}

// arrResultLine is the import's summary: "Imported 3 films · 2 files recorded · 1 already
// here · 0 skipped · 0 failed".
export function arrResultLine(r: ArrResult): string {
  const series = r.media === "series";
  const parts = [`Imported ${count(r.imported, series ? "show" : "film", series ? "shows" : "films")}`];
  if (r.files_attached > 0) parts.push(`${count(r.files_attached, series ? "episode file" : "file", series ? "episode files" : "files")} recorded`);
  parts.push(`${n(r.existing)} already here`, `${n(r.skipped)} skipped`, `${n(r.failed)} failed`);
  if (r.paced > 0) parts.push(`${n(r.paced)} wanted, searched for a few at a time`);
  // Unmonitored over there comes over that way: nothing is searched for them, which would
  // read as the import having done nothing unless it's said.
  const off = r.unmonitored ?? 0;
  if (off > 0) {
    const from = r.name || (series ? "Sonarr" : "Radarr");
    parts.push(series
      ? `${count(off, "show", "shows")} added with searching off (as they were in ${from}) — turn searching on from the Series list`
      : `${count(off, "film", "films")} added unmonitored (as they were in ${from}) — monitor them from the Movies list`);
  }
  return parts.join(" · ");
}

const OUTCOMES: { outcome: ArrNote["outcome"]; label: string }[] = [
  { outcome: "failed", label: "Failed" },
  { outcome: "skipped", label: "Skipped" },
  { outcome: "imported", label: "Imported, with a note" },
  { outcome: "attached", label: "Files given to titles already here" },
];

// notesByOutcome groups the summary's notes, failures first, leaving out empty groups.
export function notesByOutcome(notes: ArrNote[]): { outcome: ArrNote["outcome"]; label: string; notes: ArrNote[] }[] {
  return OUTCOMES.map((o) => ({ ...o, notes: notes.filter((x) => x.outcome === o.outcome) })).filter((g) => g.notes.length > 0);
}
