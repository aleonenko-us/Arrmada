import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, type Artist, type ArtistLookup } from "../../lib/api";
import { Button, Modal } from "../../ui";
import { PosterCard } from "./PosterCard";
import { RailArrows, RailStrip, useRail } from "./rail";

const DEFAULT_GENRES = ["Rock", "Pop", "Hip-hop", "Electronic", "Jazz", "Classical", "Metal", "Folk", "R&B", "Country"];

export function MusicDiscover({ canRequest }: { canRequest: boolean }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [artists, setArtists] = useState<Artist[]>([]);
  const [results, setResults] = useState<ArtistLookup[] | null>(null);
  const [resultTitle, setResultTitle] = useState("");
  const [activeGenre, setActiveGenre] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<ArtistLookup | null>(null);

  useEffect(() => {
    api.artists().then(setArtists).catch(() => setArtists([]));
  }, []);

  // Put styles already represented in this household first, so the browse controls
  // reflect the actual library instead of starting from a generic genre directory.
  const genreCounts = artists.flatMap((artist) => artist.genres ?? []).reduce<Record<string, number>>((counts, genre) => {
    const label = genre.trim().replace(/\b\w/g, (letter) => letter.toUpperCase());
    if (label) counts[label] = (counts[label] ?? 0) + 1;
    return counts;
  }, {});
  const libraryGenres = Object.entries(genreCounts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 8);
  const genres = libraryGenres.length > 0 ? libraryGenres.map(([genre]) => genre) : DEFAULT_GENRES;

  const runSearch = async (q: string, title: string) => {
    setBusy(true);
    setError(null);
    setResultTitle(title);
    setResults(null);
    try {
      setResults(await api.lookupArtists(q));
    } catch (e) {
      setResults([]);
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const search = async (event: React.FormEvent) => {
    event.preventDefault();
    const q = query.trim();
    if (!q) return;
    setActiveGenre(null);
    await runSearch(q, `Search results for “${q}”`);
  };

  const browseGenre = (genre: string) => {
    setQuery(genre);
    setActiveGenre(genre);
    // MusicBrainz artist search accepts tagged-artist queries; keep this as one request
    // per chosen genre instead of firing a burst of searches on every Discover visit.
    void runSearch(`tag:"${genre.toLowerCase()}"`, `Explore ${genre} artists`);
  };

  const add = async (artist: ArtistLookup) => {
    setAdding(artist.mbid);
    setError(null);
    try {
      const added = await api.addArtist({ mbid: artist.mbid, monitored: true });
      setArtists((current) => [...current.filter((a) => a.mbid !== added.mbid), added]);
      setSelected(null);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(null);
    }
  };

  return (
    <div className="flex flex-col gap-7">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="m-0 text-xl font-bold">Discover music</h2>
          <p className="mb-0 mt-1 text-sm text-ink-dim">Explore artists by genre or search MusicBrainz, then add artists to your library.</p>
        </div>
        <form onSubmit={search} className="flex w-full gap-2 sm:w-auto">
          <input
            aria-label="Search artists"
            value={query}
            onChange={(e) => { setQuery(e.target.value); setActiveGenre(null); }}
            placeholder="Search artists…"
            className="min-w-0 flex-1 rounded-lg px-3 py-2 text-base sm:w-[250px] sm:flex-initial"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
          />
          <Button type="submit" variant="primary" busy={busy} busyLabel="Searching…" disabled={!query.trim()}>Search</Button>
        </form>
      </div>

      {error && <div role="alert" className="rounded-lg p-3 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}

      {artists.length > 0 && <LibraryArtists artists={artists} onOpen={(id) => navigate(`/music/${id}`)} />}

      <section aria-label="Browse artists by genre">
        <div className="mb-2.5">
          <h2 className="m-0 text-lg font-bold">Explore by style</h2>
          <p className="mb-0 mt-1 text-sm text-ink-dim">{libraryGenres.length ? "Genres from your artists, with more popular styles first." : "Choose a genre to browse artists."}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          {genres.map((genre) => {
            const active = activeGenre === genre;
            return (
              <button key={genre} type="button" aria-pressed={active} disabled={busy} onClick={() => browseGenre(genre)} className="rounded-full px-3 py-1.5 text-sm font-semibold transition-colors disabled:opacity-60" style={{ background: active ? "var(--accent-soft)" : "var(--panel-2)", border: `1px solid ${active ? "var(--accent-line, var(--line))" : "var(--line)"}`, color: active ? "var(--accent-text)" : "var(--ink-dim)" }}>
                {genre}{genreCounts[genre] ? <span className="ml-1.5 opacity-70">{genreCounts[genre]}</span> : null}
              </button>
            );
          })}
        </div>
      </section>

      {results === null ? (
        <div className="rounded-xl p-6 text-base text-ink-dim" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
          Choose a genre or search for an artist to explore MusicBrainz.
        </div>
      ) : results.length === 0 ? (
        <div className="rounded-xl p-6 text-base text-ink-dim" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>No artists found for this search.</div>
      ) : (
        <ArtistResults title={resultTitle} results={results} artists={artists} canRequest={canRequest} adding={adding} onAdd={add} onOpen={setSelected} />
      )}

      {selected && (
        <Modal onClose={() => setSelected(null)} title={selected.name} size="sm" footer={(
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setSelected(null)}>Close</Button>
            {canRequest && !artists.some((a) => a.mbid === selected.mbid) && <Button variant="primary" busy={adding === selected.mbid} onClick={() => void add(selected)}>Add artist</Button>}
          </div>
        )}>
          <div className="flex items-center gap-4">
            <ArtistArt name={selected.name} />
            <div className="min-w-0">
              <h2 className="m-0 text-lg font-bold">{selected.name}</h2>
              <p className="mb-0 mt-1 text-sm text-ink-dim">{[selected.disambiguation, selected.type, selected.country].filter(Boolean).join(" · ") || "Artist"}</p>
              {artists.some((a) => a.mbid === selected.mbid) && <p className="mb-0 mt-2 text-sm font-semibold" style={{ color: "var(--good-text)" }}>Already in your library</p>}
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}

function LibraryArtists({ artists, onOpen }: { artists: Artist[]; onOpen: (id: number) => void }) {
  const rail = useRail();
  return (
    <section aria-label="Your artists">
      <div className="mb-2.5 flex items-center justify-between">
        <h2 className="m-0 text-lg font-bold">Your artists</h2>
        <RailArrows rail={rail} />
      </div>
      <RailStrip rail={rail}>
        {artists.map((artist) => (
          <PosterCard key={artist.id} aspectRatio="1/1" title={artist.name} onOpen={() => onOpen(artist.id)} badge={{ label: "In library", tone: "good" }} art={<ArtistArt name={artist.name} image={artist.image_url} />}>
            <div className="mt-0.5 truncate text-xs text-ink-faint">{artist.genres?.slice(0, 2).join(" · ") || artist.type || "Artist"}</div>
          </PosterCard>
        ))}
      </RailStrip>
    </section>
  );
}

function ArtistResults({ title, results, artists, canRequest, adding, onAdd, onOpen }: {
  title: string; results: ArtistLookup[]; artists: Artist[]; canRequest: boolean; adding: string | null;
  onAdd: (artist: ArtistLookup) => Promise<void>; onOpen: (artist: ArtistLookup) => void;
}) {
  const rail = useRail();
  return (
    <section aria-label={title}>
      <div className="mb-2.5 flex items-center justify-between">
        <h2 className="m-0 text-lg font-bold">{title}</h2>
        <RailArrows rail={rail} />
      </div>
      <RailStrip rail={rail}>
        {results.filter((artist) => artist.name.trim() && !/^(various artists|unknown)$/i.test(artist.name) && artist.type !== "Other").map((artist) => {
          const inLibrary = artists.some((a) => a.mbid === artist.mbid);
          return (
            <PosterCard key={artist.mbid} aspectRatio="1/1" title={artist.name} onOpen={() => onOpen(artist)} badge={inLibrary ? { label: "In library", tone: "good" } : null} art={<ArtistArt name={artist.name} />} quick={!inLibrary && canRequest ? { label: "＋ Add artist", busy: adding === artist.mbid, onClick: () => void onAdd(artist) } : null}>
              <div className="mt-0.5 truncate text-xs text-ink-faint" title={[artist.disambiguation, artist.type, artist.country].filter(Boolean).join(" · ")}>
                {[artist.disambiguation, artist.type, artist.country].filter(Boolean).join(" · ") || "Artist"}
              </div>
            </PosterCard>
          );
        })}
      </RailStrip>
    </section>
  );
}

function ArtistArt({ name, image }: { name: string; image?: string }) {
  return image ? (
    <img src={image} alt={name} className="h-full w-full object-cover" loading="lazy" decoding="async" onError={(e) => { e.currentTarget.style.visibility = "hidden"; }} />
  ) : (
    <div className="relative flex h-full w-full items-center justify-center overflow-hidden" style={{ background: "radial-gradient(circle at 50% 30%, var(--accent-soft), transparent 50%), linear-gradient(150deg, var(--panel-2), var(--panel))" }}>
      <div className="absolute h-28 w-28 rounded-full" style={{ border: "1px solid var(--accent-line, var(--line))", boxShadow: "0 0 44px var(--accent-soft)" }} />
      <span className="relative grid h-16 w-16 place-items-center rounded-full text-2xl font-extrabold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>{name.trim().charAt(0).toUpperCase() || "♪"}</span>
    </div>
  );
}
