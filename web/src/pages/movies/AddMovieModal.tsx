import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, type MinAvailability, type MovieLookup } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { FolderSelect } from "../../components/FolderSelect";
import { Button, Modal } from "../../ui";

// AddMovieModal is the library's "+ Add movie" (MOV-23): search TMDB, see which films are already in the
// library (a link to them, not an add that can only fail), then confirm one film with its
// quality profile, minimum availability and whether to search now. That last choice is for
// this add only: the shared default (Series and Books read it too) changes only in
// Settings, or here when "Remember as my default" is ticked.

export const AVAILABILITY: { value: MinAvailability; label: string; hint: string }[] = [
  { value: "announced", label: "Announced", hint: "search as soon as it's listed" },
  { value: "inCinemas", label: "In cinemas", hint: "search from its cinema release" },
  { value: "released", label: "Released", hint: "search once it's out on disc or streaming" },
];

const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

// folderId preselects a folder: the library page is showing that one folder, so what's
// added there goes into it. Left out, the folder rules preselect as usual.
export function AddMovieModal({ onClose, onAdded, folderId }: { onClose: () => void; onAdded: () => void; folderId?: number }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<MovieLookup[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [profile, setProfile] = useState("");
  const [defaultSearch, setDefaultSearch] = useState(true); // the shared search-on-add default
  const [picked, setPicked] = useState<MovieLookup | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    qualityApi.qualityProfiles("movie")
      .then((r) => {
        setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
        const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
        if (def) setProfile(def.key);
      })
      .catch(() => {});
    api.settings().then((s) => setDefaultSearch(s.search_on_add)).catch(() => {});
  }, []);

  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!q.trim()) return;
    setLoading(true);
    setError(null);
    try {
      setResults(await api.lookupMovies(q.trim()));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal onClose={onClose} size="lg" title={picked ? "Add this movie" : "Add a movie"} initialFocus={inputRef}>
      {picked ? (
        <ConfirmAdd
          film={picked}
          profiles={profiles}
          profile={profile}
          onProfile={setProfile}
          defaultSearch={defaultSearch}
          folderId={folderId}
          onBack={() => setPicked(null)}
          onAdded={() => { onAdded(); onClose(); }}
        />
      ) : (
        <>
          <form onSubmit={search} className="mb-3 flex gap-2">
            <input ref={inputRef} aria-label="Search TMDB for a movie" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search movies — e.g. Dune Part Two" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-md" style={field} />
            <Button type="submit" variant="primary" busy={loading} busyLabel="…">Search</Button>
          </form>
          {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
          <div className="max-h-[52vh] overflow-y-auto">
            {results.map((r) => (
              <ResultRow key={r.tmdb_id} r={r} onPick={() => setPicked(r)} onOpen={onClose} />
            ))}
          </div>
        </>
      )}
    </Modal>
  );
}

function Poster({ url }: { url?: string }) {
  return (
    <div className="h-[68px] w-[46px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
      {url && <img src={url} alt="" className="h-full w-full object-cover" loading="lazy" />}
    </div>
  );
}

// ResultRow is one search hit: a film already in the library links to it; any other opens
// the confirm step (never an add on a single click).
function ResultRow({ r, onPick, onOpen }: { r: MovieLookup; onPick: () => void; onOpen: () => void }) {
  const body = (
    <>
      <Poster url={r.poster_url} />
      <div className="min-w-0 flex-1">
        <div className="text-md font-semibold">{r.title} <span className="font-normal text-ink-faint">{r.year ? `(${r.year})` : ""}</span></div>
        <div className="mt-0.5 line-clamp-2 text-sm text-ink-dim">{r.overview || "No overview."}</div>
      </div>
    </>
  );
  if (r.in_library && r.library_id) {
    return (
      <Link to={`/movies/${r.library_id}`} onClick={onOpen} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
        {body}
        <span className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--good-soft)", color: "var(--good-text)" }}>In library →</span>
      </Link>
    );
  }
  return (
    <button onClick={onPick} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
      {body}
      <span className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>Add…</span>
    </button>
  );
}

function ConfirmAdd({ film, profiles, profile, onProfile, defaultSearch, folderId, onBack, onAdded }: {
  film: MovieLookup;
  profiles: { key: string; name: string }[];
  profile: string;
  onProfile: (p: string) => void;
  defaultSearch: boolean;
  folderId?: number;
  onBack: () => void;
  onAdded: () => void;
}) {
  const [availability, setAvailability] = useState<MinAvailability>("released");
  const [folder, setFolder] = useState<number | undefined>(folderId);
  const [searchNow, setSearchNow] = useState(defaultSearch);
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { setSearchNow(defaultSearch); }, [defaultSearch]);

  const add = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.addMovie({ tmdb_id: film.tmdb_id, quality_profile: profile, monitored: true, search_on_add: searchNow, min_availability: availability, folder_id: folder });
      // Only an explicit tick changes the shared default.
      if (remember && searchNow !== defaultSearch) await api.updateSettings({ search_on_add: searchNow }).catch(() => {});
      onAdded();
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <Poster url={film.poster_url} />
        <div className="min-w-0">
          <div className="text-md font-semibold">{film.title} <span className="font-normal text-ink-faint">{film.year ? `(${film.year})` : ""}</span></div>
          <div className="mt-0.5 line-clamp-2 text-sm text-ink-dim">{film.overview || "No overview."}</div>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">Quality profile</span>
          <select value={profile} onChange={(e) => onProfile(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-base" style={field}>
            {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">Minimum availability</span>
          <select value={availability} onChange={(e) => setAvailability(e.target.value as MinAvailability)} className="rounded-lg px-2.5 py-1.5 text-base" style={field}>
            {AVAILABILITY.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
          </select>
          <span className="text-xs text-ink-faint">{AVAILABILITY.find((a) => a.value === availability)?.hint}</span>
        </label>
        {/* Only with more than one Movies folder; otherwise nothing renders. */}
        <FolderSelect media="movie" tmdbId={film.tmdb_id} value={folder} onChange={setFolder} disabled={busy} compact />
      </div>

      <fieldset className="mt-4 flex flex-col gap-2 text-base">
        <legend className="mb-1 font-mono text-2xs uppercase text-ink-faint">When it's added</legend>
        <label className="flex items-start gap-2.5">
          <input type="radio" name="add-search" className="mt-0.5" checked={searchNow} onChange={() => setSearchNow(true)} />
          <span>Monitor it and search now</span>
        </label>
        <label className="flex items-start gap-2.5">
          <input type="radio" name="add-search" className="mt-0.5" checked={!searchNow} onChange={() => setSearchNow(false)} />
          <span>
            Add unmonitored
            <span className="mt-0.5 block text-sm text-ink-faint">Nothing is searched or grabbed until you monitor it.</span>
          </span>
        </label>
        {searchNow !== defaultSearch && (
          <label className="mt-1 flex items-start gap-2.5 text-sm text-ink-dim">
            <input type="checkbox" className="mt-0.5" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            <span>Remember as my default (Series and Books use it too)</span>
          </label>
        )}
      </fieldset>

      {error && <div className="mt-3 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy}>Back</Button>
        <Button variant="primary" onClick={add} disabled={busy}>{busy ? "Adding…" : "Add movie"}</Button>
      </div>
    </div>
  );
}
