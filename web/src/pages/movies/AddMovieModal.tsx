import { useEffect, useState } from "react";
import { api, type MovieLookup } from "../../lib/api";

// AddMovieModal is the library's "+ Add movie": search TMDB, pick a film, and add it on a
// profile, searching for it at once or not.
export function AddMovieModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<MovieLookup[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profile, setProfile] = useState("");
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [searchOnAdd, setSearchOnAdd] = useState(true);
  const [addingId, setAddingId] = useState<number | null>(null);

  useEffect(() => {
    api
      .qualityProfiles("movie")
      .then((r) => {
        setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
        const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
        if (def) setProfile(def.key);
      })
      .catch(() => {});
    api.settings().then((s) => setSearchOnAdd(s.search_on_add)).catch(() => {});
  }, []);

  // Persist the preference so it sticks across sessions and devices.
  const toggleSearchOnAdd = () => {
    const next = !searchOnAdd;
    setSearchOnAdd(next);
    api.updateSettings({ search_on_add: next }).catch(() => {});
  };

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

  const add = async (r: MovieLookup) => {
    setAddingId(r.tmdb_id);
    setError(null);
    try {
      await api.addMovie({ tmdb_id: r.tmdb_id, quality_profile: profile, monitored: true, search_on_add: searchOnAdd });
      onAdded();
      onClose();
    } catch (err) {
      setError((err as Error).message);
      setAddingId(null);
    }
  };

  return (
    <div className="fixed inset-0 z-50 grid place-items-start justify-center p-6" style={{ background: "rgba(0,0,0,.55)" }} onClick={onClose}>
      <div className="mt-12 w-full max-w-[640px] rounded-2xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)", boxShadow: "var(--shadow)" }} onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 className="m-0 text-[15px] font-bold">Add a movie</h2>
          <div className="flex items-center gap-4">
            <button type="button" onClick={toggleSearchOnAdd} title="On: monitor and search for a release right away. Off: add unmonitored — nothing is searched or grabbed until you monitor it." className="flex items-center gap-2">
              <span className="relative inline-block h-[20px] w-[34px] rounded-full transition-colors" style={{ background: searchOnAdd ? "var(--accent)" : "var(--line)" }}>
                <span className="absolute top-[3px] h-[14px] w-[14px] rounded-full bg-white transition-all" style={{ left: searchOnAdd ? "17px" : "3px" }} />
              </span>
              <span className="text-[11.5px] font-medium" style={{ color: searchOnAdd ? "var(--ink)" : "var(--ink-dim)" }}>{searchOnAdd ? "Search on add" : "Add unmonitored"}</span>
            </button>
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] uppercase text-ink-faint">Quality</span>
              <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2 py-1.5 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
                {profiles.map((p) => (
                  <option key={p.key} value={p.key}>{p.name}</option>
                ))}
              </select>
            </div>
          </div>
        </div>

        <form onSubmit={search} className="mb-3 flex gap-2">
          <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search movies — e.g. Dune Part Two" className="flex-1 rounded-lg px-3 py-2 text-[13px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          <button type="submit" disabled={loading} className="rounded-lg px-4 py-2 text-[12.5px] font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
            {loading ? "…" : "Search"}
          </button>
        </form>

        {error && <div className="mb-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}

        <div className="max-h-[52vh] overflow-y-auto">
          {results.map((r) => (
            <button
              key={r.tmdb_id}
              onClick={() => add(r)}
              disabled={addingId !== null}
              className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-[var(--panel-2)]"
            >
              <div className="h-[68px] w-[46px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
                {r.poster_url && <img src={r.poster_url} alt="" className="h-full w-full object-cover" loading="lazy" />}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-semibold">{r.title} <span className="font-normal text-ink-faint">{r.year ? `(${r.year})` : ""}</span></div>
                <div className="mt-0.5 line-clamp-2 text-[11.5px] text-ink-dim">{r.overview || "No overview."}</div>
              </div>
              <span className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>
                {addingId === r.tmdb_id ? "Adding…" : "Add"}
              </span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
