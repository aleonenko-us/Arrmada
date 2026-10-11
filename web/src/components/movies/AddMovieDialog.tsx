import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type MinAvailability, type MovieLookup } from "../../lib/api";
import { Button, Modal } from "../../ui";

// The staff "Add movie" flow (MOV-23): search TMDB, see which films are already in the
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

export function AddMovieDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<MovieLookup[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [profile, setProfile] = useState("");
  const [defaultSearch, setDefaultSearch] = useState(true); // the shared search-on-add default
  const [picked, setPicked] = useState<MovieLookup | null>(null);

  useEffect(() => {
    api.qualityProfiles("movie")
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
    <Modal onClose={onClose} size="md" title={picked ? "Add this movie" : "Add a movie"}>
      {picked ? (
        <ConfirmAdd
          film={picked}
          profiles={profiles}
          profile={profile}
          onProfile={setProfile}
          defaultSearch={defaultSearch}
          onBack={() => setPicked(null)}
          onAdded={() => { onAdded(); onClose(); }}
        />
      ) : (
        <>
          <form onSubmit={search} className="mb-3 flex gap-2">
            {/* eslint-disable-next-line jsx-a11y/no-autofocus -- the dialog exists to type a title */}
            <input autoFocus aria-label="Movie title" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search movies — e.g. Dune Part Two" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-[13px]" style={field} />
            <Button type="submit" variant="primary" disabled={loading}>{loading ? "…" : "Search"}</Button>
          </form>
          {error && <div className="mb-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
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
        <div className="text-[13px] font-semibold">{r.title} <span className="font-normal text-ink-faint">{r.year ? `(${r.year})` : ""}</span></div>
        <div className="mt-0.5 line-clamp-2 text-[11.5px] text-ink-dim">{r.overview || "No overview."}</div>
      </div>
    </>
  );
  if (r.in_library && r.library_id) {
    return (
      <Link to={`/movies/${r.library_id}`} onClick={onOpen} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
        {body}
        <span className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--good-soft)", color: "var(--good)" }}>In library →</span>
      </Link>
    );
  }
  return (
    <button onClick={onPick} className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]">
      {body}
      <span className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>Add…</span>
    </button>
  );
}

function ConfirmAdd({ film, profiles, profile, onProfile, defaultSearch, onBack, onAdded }: {
  film: MovieLookup;
  profiles: { key: string; name: string }[];
  profile: string;
  onProfile: (p: string) => void;
  defaultSearch: boolean;
  onBack: () => void;
  onAdded: () => void;
}) {
  const [availability, setAvailability] = useState<MinAvailability>("released");
  const [searchNow, setSearchNow] = useState(defaultSearch);
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { setSearchNow(defaultSearch); }, [defaultSearch]);

  const add = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.addMovie({ tmdb_id: film.tmdb_id, quality_profile: profile, monitored: true, search_on_add: searchNow, min_availability: availability });
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
          <div className="text-[14px] font-semibold">{film.title} <span className="font-normal text-ink-faint">{film.year ? `(${film.year})` : ""}</span></div>
          <div className="mt-0.5 line-clamp-2 text-[11.5px] text-ink-dim">{film.overview || "No overview."}</div>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-[12px]">
          <span className="font-mono text-[10px] uppercase text-ink-faint">Quality profile</span>
          <select value={profile} onChange={(e) => onProfile(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-[12.5px]" style={field}>
            {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-[12px]">
          <span className="font-mono text-[10px] uppercase text-ink-faint">Minimum availability</span>
          <select value={availability} onChange={(e) => setAvailability(e.target.value as MinAvailability)} className="rounded-lg px-2.5 py-1.5 text-[12.5px]" style={field}>
            {AVAILABILITY.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
          </select>
          <span className="text-[10.5px] text-ink-faint">{AVAILABILITY.find((a) => a.value === availability)?.hint}</span>
        </label>
      </div>

      <fieldset className="mt-4 flex flex-col gap-2 text-[12.5px]">
        <legend className="mb-1 font-mono text-[10px] uppercase text-ink-faint">When it's added</legend>
        <label className="flex items-start gap-2.5">
          <input type="radio" name="add-search" className="mt-0.5" checked={searchNow} onChange={() => setSearchNow(true)} />
          <span>Monitor it and search now</span>
        </label>
        <label className="flex items-start gap-2.5">
          <input type="radio" name="add-search" className="mt-0.5" checked={!searchNow} onChange={() => setSearchNow(false)} />
          <span>
            Add unmonitored
            <span className="mt-0.5 block text-[11.5px] text-ink-faint">Nothing is searched or grabbed until you monitor it.</span>
          </span>
        </label>
        {searchNow !== defaultSearch && (
          <label className="mt-1 flex items-start gap-2.5 text-[12px] text-ink-dim">
            <input type="checkbox" className="mt-0.5" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            <span>Remember as my default (Series and Books use it too)</span>
          </label>
        )}
      </fieldset>

      {error && <div className="mt-3 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy}>Back</Button>
        <Button variant="primary" onClick={add} disabled={busy}>{busy ? "Adding…" : "Add movie"}</Button>
      </div>
    </div>
  );
}
