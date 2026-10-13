import { useEffect, useId, useState } from "react";
import { ISSUE_CATEGORIES, ISSUE_NOTE_MAX, issuesApi, type IssueCategory, type IssueSeasonFiles } from "../lib/issuesApi";
import { Button, Sheet, useToast } from "../ui";

// IssueReportSheet is "Report a problem" for a title in the library: what's wrong (picture,
// sound, subtitles, wrong file, something else), which episode for a show, and an optional
// note. It's its own chunk, with its API calls, loaded only when someone taps the button on
// the title's sheet. The server checks the title (and episode) is in the library with a
// file; a second report of the same problem joins the first.
export function IssueReportSheet({ media, tmdbId, title, onClose }: {
  media: "movie" | "series";
  tmdbId: number;
  title: string;
  onClose: () => void;
}) {
  const toast = useToast();
  const id = useId();
  const [category, setCategory] = useState<IssueCategory | "">("");
  const [note, setNote] = useState("");
  // A show's episodes that have a file, by season; "error" when they couldn't be read.
  const [seasons, setSeasons] = useState<IssueSeasonFiles[] | "error" | null>(media === "series" ? null : []);
  const [season, setSeason] = useState(-1);
  const [episode, setEpisode] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (media !== "series") return;
    let alive = true;
    issuesApi.episodes(tmdbId)
      .then((r) => {
        if (!alive) return;
        setSeasons(r.seasons);
        // Start on the latest season: what someone is most likely watching.
        const last = r.seasons[r.seasons.length - 1];
        if (last) setSeason(last.season);
      })
      .catch(() => { if (alive) setSeasons("error"); });
    return () => { alive = false; };
  }, [media, tmdbId]);

  const eps = Array.isArray(seasons) ? seasons.find((s) => s.season === season)?.episodes ?? [] : [];
  const ready = !!category && (media === "movie" || episode > 0);

  const send = async () => {
    if (!ready || !category) return;
    setBusy(true); setError(null);
    try {
      const r = await issuesApi.report({
        media_type: media, tmdb_id: tmdbId, category,
        ...(media === "series" ? { season, episode } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      });
      toast(r.joined ? "Thanks — someone reported that too. We’ll let you know when it’s fixed." : "Thanks — we’ll let you know when it’s fixed.");
      onClose();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };
  return (
    <Sheet onClose={onClose} ariaLabel={`Report a problem with ${title}`} size="md" dismissible={!busy}>
      <div className="flex flex-col gap-4 p-5 sm:p-6">
        <div>
          <h2 className="m-0 text-xl font-bold leading-tight">Report a problem</h2>
          <p className="m-0 mt-1 text-base text-ink-dim">with {title}. We’ll take a look and let you know when it’s fixed.</p>
        </div>

        {media === "series" && (
          seasons === null ? (
            <div className="text-sm text-ink-faint">Loading episodes…</div>
          ) : seasons === "error" || seasons.length === 0 ? (
            <div className="text-sm" style={{ color: "var(--reject)" }}>
              {seasons === "error" ? "Couldn’t load the episodes — try again in a moment." : "There are no episodes of this show to report yet."}
            </div>
          ) : (
            <div className="flex flex-wrap gap-3">
              <label className="flex min-w-[120px] flex-1 flex-col gap-1 text-sm">
                <span className="font-mono text-2xs uppercase text-ink-faint">Season</span>
                <select aria-label="Season" value={season} onChange={(e) => { setSeason(Number(e.target.value)); setEpisode(0); }} className="min-h-[40px] rounded-lg px-3 text-md" style={field}>
                  {seasons.map((s) => <option key={s.season} value={s.season}>{s.season === 0 ? "Specials" : `Season ${s.season}`}</option>)}
                </select>
              </label>
              <label className="flex min-w-[160px] flex-[2] flex-col gap-1 text-sm">
                <span className="font-mono text-2xs uppercase text-ink-faint">Episode</span>
                <select aria-label="Episode" value={episode} onChange={(e) => setEpisode(Number(e.target.value))} className="min-h-[40px] rounded-lg px-3 text-md" style={field}>
                  <option value={0}>Pick an episode</option>
                  {eps.map((e) => <option key={e.episode} value={e.episode}>{`Episode ${e.episode}${e.title ? ` — ${e.title}` : ""}`}</option>)}
                </select>
              </label>
            </div>
          )
        )}

        <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="mb-1.5 font-mono text-2xs uppercase text-ink-faint">What’s wrong?</legend>
          {ISSUE_CATEGORIES.map((c) => {
            const on = category === c.key;
            return (
              <label key={c.key} aria-label={`${c.label}: ${c.hint}`} className="flex min-h-[44px] cursor-pointer items-center gap-3 rounded-lg px-3 py-2"
                style={{ background: on ? "var(--accent-soft)" : "var(--panel-2)", border: `1px solid ${on ? "var(--accent)" : "var(--line)"}` }}>
                <input type="radio" name={`${id}-category`} value={c.key} checked={on} onChange={() => setCategory(c.key)} className="h-4 w-4 flex-none" />
                <span className="min-w-0">
                  <span className="block text-md font-semibold" style={{ color: on ? "var(--accent)" : "var(--ink)" }}>{c.label}</span>
                  <span className="block text-xs text-ink-faint">{c.hint}</span>
                </span>
              </label>
            );
          })}
        </fieldset>

        <label className="flex flex-col gap-1 text-sm text-ink-dim">
          <span className="font-mono text-2xs uppercase text-ink-faint">Note (optional)</span>
          <textarea
            value={note}
            onChange={(e) => setNote(e.target.value.slice(0, ISSUE_NOTE_MAX))}
            maxLength={ISSUE_NOTE_MAX}
            rows={3}
            placeholder="For example: no English audio from 10 minutes in"
            className="rounded-lg px-3 py-2 text-md"
            style={field}
          />
          <span className="self-end font-mono text-xs text-ink-faint">{note.length}/{ISSUE_NOTE_MAX}</span>
        </label>

        {error && <div className="text-sm font-medium" style={{ color: "var(--reject)" }} role="alert">{error}</div>}

        <div className="flex flex-wrap gap-2">
          <Button variant="primary" className="min-h-[44px] flex-1 sm:flex-none" onClick={() => { void send(); }} busy={busy} busyLabel="Sending…" disabled={!ready}>Send report</Button>
          <Button variant="ghost" className="min-h-[44px]" onClick={onClose} disabled={busy}>Cancel</Button>
        </div>
      </div>
    </Sheet>
  );
}
