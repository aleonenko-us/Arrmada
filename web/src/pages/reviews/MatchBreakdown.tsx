import { Link } from "react-router-dom";
import type { MusicMatchCandidate as MatchCandidate, MusicMatchReport as MatchReport } from "../../lib/api";

// MatchBreakdown says in words why the music matcher held a download (MUS-31): the
// editions it weighed, how far each was from the files and which fields made up the
// distance, what didn't fit track by track, and the files it would leave behind.

// What each part of the distance compares, in the words a person would use.
const COMPONENT_WORDS: Record<string, string> = {
  album_id: "MusicBrainz release id",
  artist: "album artist",
  album: "album title",
  tracks: "track titles, numbers and lengths",
  missing_tracks: "tracks with no file",
  unmatched_tracks: "files with no track",
  data_source: "metadata source",
  media_count: "number of discs",
  media_format: "disc format",
  year: "year",
  country: "country",
  label: "record label",
  catalog_number: "catalogue number",
  album_disambiguation: "edition note",
  barcode: "barcode",
};

// The parts that added to a candidate's distance, biggest first.
export function distanceParts(c: MatchCandidate): string[] {
  return [...c.components]
    .filter((p) => p.share >= 0.005)
    .sort((a, b) => b.share - a.share)
    .map((p) => `${COMPONENT_WORDS[p.name] ?? p.name.replace(/_/g, " ")} +${p.share.toFixed(2)}`);
}

function CandidateBlock({ c, best }: { c: MatchCandidate; best: boolean }) {
  const parts = distanceParts(c);
  return (
    <div className="mt-2 rounded-lg px-3 py-2" style={{ border: "1px solid var(--line-soft)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-baseline gap-x-2 text-base">
        <b>{c.album}</b>
        <span className="text-ink-dim">{c.edition}</span>
        {c.target && <span className="font-mono text-2xs uppercase text-ink-faint">the album's edition</span>}
        <span className="ml-auto font-mono text-xs" style={{ color: best ? "var(--accent-text)" : "var(--ink-faint)" }}>
          distance {c.distance.toFixed(2)}
        </span>
      </div>
      {parts.length > 0 && <div className="mt-1 text-sm text-ink-dim">Made up of: {parts.join(" · ")}</div>}
      {c.tracks && c.tracks.length > 0 && (
        <ul className="m-0 mt-1 list-none p-0 text-sm">
          {c.tracks.map((t) => (
            <li key={t.file + t.track} className="text-ink-dim">
              <span className="font-mono text-xs">Track {t.track}</span> ← <span className="font-mono text-xs">{t.file}</span>: {(t.notes ?? []).join("; ")}
            </li>
          ))}
        </ul>
      )}
      {c.missing && c.missing.length > 0 && (
        <div className="mt-1 text-sm text-ink-dim">No file for: {c.missing.join(", ")}</div>
      )}
      {c.extra && c.extra.length > 0 && <div className="mt-1 text-sm text-ink-dim">No track for: {c.extra.join(", ")}</div>}
    </div>
  );
}

export function MatchBreakdown({ match }: { match: MatchReport }) {
  const cands = match.candidates ?? [];
  return (
    <div className="mt-2" data-testid="match-breakdown">
      {cands.length > 0 && (
        <p className="m-0 text-sm text-ink-dim">
          {match.expected ? "Grabbed for this album" : "Nobody grabbed this for an album"}, so it imports by itself up to a distance of{" "}
          {match.threshold.toFixed(2)} (0 is a perfect fit).
        </p>
      )}
      {cands.map((c, i) => (
        <CandidateBlock key={`${c.album_id}-${c.release_id}`} c={c} best={i === 0} />
      ))}
      {match.unplaced && match.unplaced.length > 0 && (
        <div className="mt-2 text-sm text-ink-dim">
          Would stay in the download: {match.unplaced.map((u) => `${u.file} (${u.why})`).join(", ")}
        </div>
      )}
      {match.fingerprint === "off" && (
        <div className="mt-2 text-sm text-ink-dim">
          Add an AcoustID key in{" "}
          <Link to="/settings/connections#apikey-acoustid" className="font-semibold" style={{ color: "var(--accent-text)" }}>
            Settings → Connections
          </Link>{" "}
          to identify files by their sound.
        </div>
      )}
    </div>
  );
}
