import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type UnmatchedFolder, type MatchCandidate } from "../lib/api";
import { foldersApi } from "../lib/foldersApi";
import { useToast } from "../ui";

// UnmatchedReview lists the folders a library scan left needing you, each with why in plain
// words and the way to fix it: pick the right title (candidates or a search), open the show
// it was added as, or open Add series with the folder's name filled in. It's Settings →
// Library's needs-review list and the report under the Getting started import step.

type Media = "movie" | "series";

export function UnmatchedReview({ media, reloadKey, onChanged }: { media: Media; reloadKey: number; onChanged?: () => void }) {
  const toast = useToast();
  const label = media === "movie" ? "Movies" : "TV Shows";
  const [items, setItems] = useState<UnmatchedFolder[] | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () =>
    (media === "movie" ? api.moviesUnmatched() : api.seriesUnmatched()).then(setItems).catch(() => setItems([]));
  useEffect(() => { load(); }, [reloadKey]); // eslint-disable-line react-hooks/exhaustive-deps

  const pick = async (folder: string, tmdb_id: number, root?: string) => {
    setBusy(true);
    try {
      await foldersApi.importFolder(media, folder, tmdb_id, root);
      setItems((xs) => (xs ?? []).filter((u) => u.folder !== folder || u.root !== root));
      toast(`Imported “${folder}”.`, { tone: "good" });
      onChanged?.();
    } catch (e) { toast((e as Error).message, { tone: "error", ms: 6000 }); } finally { setBusy(false); }
  };

  if (items === null || items.length === 0) return null;
  return (
    <div className="mt-4 rounded-xl p-3.5" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-md font-semibold">
            {label} — needs you
            <span className="ml-1.5 rounded-full px-1.5 py-0.5 text-xs font-bold" style={{ background: "var(--reject-soft)", color: "var(--reject-text)" }}>{items.length}</span>
          </div>
          <div className="text-xs text-ink-faint">Arrmada couldn't import these on its own. Each says why; fix it here so nothing is mis-filed.</div>
        </div>
        <button onClick={load} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>Refresh</button>
      </div>
      <div className="mt-3 flex flex-col gap-2.5">
        {items.map((u) => <UnmatchedRow key={`${u.root ?? ""}/${u.folder}`} media={media} item={u} busy={busy} onPick={pick} />)}
      </div>
    </div>
  );
}

// What each reason offers. "pick" is choosing the right match; "show" opens the show the
// folder already became; none means there is nothing to pick (the folder holds no video).
function fixFor(media: Media, reason: UnmatchedFolder["reason"]): "pick" | "show" | "none" {
  if (media !== "series") return "pick";
  if (reason === "no_episodes" || reason === "duplicate") return "show";
  if (reason === "no_video") return "none";
  return "pick";
}

const GENERIC = "Arrmada couldn't confidently tell which title this folder is. Pick the right match.";

export function UnmatchedRow({ media, item, busy, onPick }: { media: Media; item: UnmatchedFolder; busy: boolean; onPick: (folder: string, tmdb: number, root?: string) => void }) {
  const [q, setQ] = useState(item.title);
  const [results, setResults] = useState<MatchCandidate[] | null>(null);
  const [searching, setSearching] = useState(false);
  const fix = fixFor(media, item.reason);

  const search = async () => {
    if (!q.trim()) return;
    setSearching(true);
    try {
      const r = media === "movie" ? await api.lookupMovies(q) : await api.lookupSeries(q);
      setResults(r.map((x) => ({ tmdb_id: x.tmdb_id, title: x.title, year: x.year, poster_url: x.poster_url })));
    } catch { setResults([]); } finally { setSearching(false); }
  };
  const shown = results ?? item.candidates ?? [];
  const addLink = `/series?find=${encodeURIComponent(item.title || item.folder)}`;

  return (
    <div className="rounded-lg p-2.5" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex items-center gap-2 text-sm">
        <span style={{ color: "var(--accent-text)" }}>📁</span>
        <span className="font-mono font-semibold">{item.folder}</span>
      </div>
      <p className="m-0 mt-1 text-sm text-ink-dim">{item.detail || GENERIC}</p>
      {fix === "show" && item.series_id ? (
        <div className="mt-2">
          <Link to={`/series/${item.series_id}`} className="rounded-lg px-2.5 py-1 text-xs font-semibold no-underline" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>Open the show →</Link>
        </div>
      ) : null}
      {fix === "pick" && (
        <>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {shown.length === 0 && <span className="text-xs text-ink-faint">No candidates — search a different title below.</span>}
            {shown.map((c) => (
              <button
                key={c.tmdb_id}
                disabled={busy}
                onClick={() => onPick(item.folder, c.tmdb_id, item.root)}
                title={c.overview}
                className="flex items-center gap-2 rounded-lg px-2 py-1 text-left text-sm disabled:opacity-50"
                style={{ border: "1px solid var(--line)", background: "var(--panel)" }}
              >
                {c.poster_url
                  ? <img src={c.poster_url} alt="" className="h-9 w-6 flex-none rounded object-cover" />
                  : <span className="grid h-9 w-6 flex-none place-items-center rounded text-xs text-ink-faint" style={{ background: "var(--panel-2)" }}>?</span>}
                <span><span className="font-semibold">{c.title}</span>{c.year ? <span className="text-ink-faint"> ({c.year})</span> : ""}</span>
              </button>
            ))}
          </div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && search()}
              placeholder="Search a different title…"
              aria-label="Search a different title"
              className="min-w-0 flex-1 rounded-lg px-2 py-1 text-sm"
              style={{ background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" }}
            />
            <button onClick={search} disabled={searching} className="rounded-lg px-2.5 py-1 text-xs font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel)", color: "var(--ink)" }}>{searching ? "…" : "Search"}</button>
            {media === "series" && <Link to={addLink} className="rounded-lg px-2.5 py-1 text-xs font-semibold no-underline" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>Open Add series</Link>}
          </div>
        </>
      )}
    </div>
  );
}
