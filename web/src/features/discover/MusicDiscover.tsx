import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type Artist, type ArtistLookup } from "../../lib/api";
import { Button } from "../../ui";

export function MusicDiscover({ canRequest }: { canRequest: boolean }) {
  const [query, setQuery] = useState("");
  const [artists, setArtists] = useState<Artist[]>([]);
  const [results, setResults] = useState<ArtistLookup[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.artists().then(setArtists).catch(() => setArtists([]));
  }, []);

  const search = async (event: React.FormEvent) => {
    event.preventDefault();
    const q = query.trim();
    if (!q) return;
    setBusy(true);
    setError(null);
    try {
      setResults(await api.lookupArtists(q));
    } catch (e) {
      setResults([]);
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const add = async (artist: ArtistLookup) => {
    setAdding(artist.mbid);
    setError(null);
    try {
      const added = await api.addArtist({ mbid: artist.mbid, monitored: true });
      setArtists((current) => [...current.filter((a) => a.mbid !== added.mbid), added]);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(null);
    }
  };

  return (
    <section className="mx-auto w-full max-w-4xl">
      <div className="mb-5">
        <h2 className="m-0 text-xl font-bold">Discover music</h2>
        <p className="mb-0 mt-1 text-sm text-ink-dim">Find artists in MusicBrainz, then add them to your music library.</p>
      </div>
      <form onSubmit={search} className="mb-5 flex gap-2">
        <input
          aria-label="Search artists"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search artists…"
          className="min-w-0 flex-1 rounded-lg px-3 py-2 text-base"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
        />
        <Button type="submit" variant="primary" busy={busy} busyLabel="Searching…" disabled={!query.trim()}>Search</Button>
      </form>
      {error && <div role="alert" className="mb-4 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}
      {results === null ? (
        <div className="rounded-xl p-8 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>
          Search for an artist to explore their music.
          {artists.length > 0 && <p className="mb-0 mt-2">Your library has {artists.length} artist{artists.length === 1 ? "" : "s"}. <Link to="/music" className="font-semibold text-accent-text">Open Music</Link></p>}
        </div>
      ) : results.length === 0 ? (
        <div className="rounded-xl p-8 text-center text-base text-ink-dim" style={{ border: "1px solid var(--line)" }}>No artists found.</div>
      ) : (
        <div className="overflow-hidden rounded-xl" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
          {results.map((artist) => {
            const inLibrary = artists.some((a) => a.mbid === artist.mbid);
            return (
              <article key={artist.mbid} className="flex items-center gap-4 border-b px-4 py-3 last:border-b-0" style={{ borderColor: "var(--line)" }}>
                <div className="flex h-11 w-11 flex-none items-center justify-center rounded-full text-sm font-bold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>{artist.name.slice(0, 1).toUpperCase()}</div>
                <div className="min-w-0 flex-1">
                  <h3 className="m-0 truncate text-base font-semibold">{artist.name}</h3>
                  <p className="mb-0 mt-0.5 truncate text-sm text-ink-dim">{[artist.disambiguation, artist.type, artist.country].filter(Boolean).join(" · ") || "Artist"}</p>
                </div>
                {inLibrary ? <span className="text-sm font-semibold text-ink-dim">In library</span> : canRequest ? (
                  <Button variant="secondary" size="sm" disabled={adding !== null} busy={adding === artist.mbid} onClick={() => add(artist)}>Add artist</Button>
                ) : <span className="text-sm text-ink-faint">Ask your admin to add</span>}
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
