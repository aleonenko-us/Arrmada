import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { api, type Artist, type ArtistLookup } from "../lib/api";
import { qualityApi } from "../lib/qualityApi";
import { jobFailed, jobToast, useJob } from "../lib/useJob";
import { useSearchParamState } from "../lib/useSearchState";
import { isWanted, libraryStatus } from "../lib/status";
import { Button, FilterBar, IconButton, LibraryToolbar, Modal, Skeleton, StatusChip, TONE_HUE, useConfirm, useToast } from "../ui";

const FILTERS = [
  { key: "all", label: "All" },
  { key: "monitored", label: "Monitored" },
  { key: "wanted", label: "Wanted" },
  { key: "complete", label: "Complete" },
] as const;
type FilterKey = (typeof FILTERS)[number]["key"];
const FILTER_KEYS = FILTERS.map((f) => f.key);

function have(a: Artist): number {
  return a.stats?.have_tracks ?? 0;
}
function total(a: Artist): number {
  return a.stats?.tracks ?? 0;
}
function isComplete(a: Artist): boolean {
  return total(a) > 0 && have(a) >= total(a);
}
function statusInput(a: Artist) {
  return { multi: true, hasFile: have(a) > 0, monitored: a.monitored, have: have(a), total: total(a) };
}

function matches(a: Artist, f: FilterKey): boolean {
  switch (f) {
    case "monitored":
      return a.monitored;
    case "wanted":
      return isWanted(statusInput(a));
    case "complete":
      return isComplete(a);
    default:
      return true;
  }
}

export function Music() {
  const [artists, setArtists] = useState<Artist[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useSearchParamState<FilterKey>("filter", "all", { allowed: FILTER_KEYS });
  const [query, setQuery] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  const [adding, setAdding] = useState(false);
  const [multiSelect, setMultiSelect] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);

  const toast = useToast();
  const confirm = useConfirm();
  // err shows the toast in the error colour: a scan that failed says so plainly.
  const say = (m: string, err = false) => toast(m, { tone: err ? "error" : "info", ms: 3500 });
  // The scan runs as a job; when it ends the toast says what it found.
  const [scanJob, setScanJob] = useState<number | null>(null);
  const scan = useJob(scanJob, {
    onDone: (j) => { setScanJob(null); load(); say(jobToast(j, "Scan finished."), jobFailed(j)); },
  });
  const load = () =>
    api
      .artists()
      .then(setArtists)
      .catch((e: Error) => {
        setError(e.message);
        setArtists([]);
      });
  useEffect(() => {
    load();
    qualityApi.qualityProfiles("music")
      .then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name }))))
      .catch(() => {});
  }, []);

  const toggleSelect = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const clearSelect = () => setSelected(new Set());
  const exitSelect = () => {
    setMultiSelect(false);
    clearSelect();
  };

  // Partial failures are reported rather than swallowed. Promise.all rejects on the first
  // error, which skips the refresh and leaves the UI showing stale state for the ones that
  // DID change — allSettled lets us apply what worked and say what didn't.
  const runBulk = async (label: string, fn: (id: number) => Promise<unknown>) => {
    const ids = [...selected];
    setBulkBusy(true);
    try {
      const results = await Promise.allSettled(ids.map(fn));
      const failed = results.filter((r) => r.status === "rejected").length;
      if (failed === 0) say(`${label} ${ids.length} artist${ids.length === 1 ? "" : "s"}.`);
      else say(`${label} ${ids.length - failed} of ${ids.length}; ${failed} failed.`, true);
      clearSelect();
      load();
    } finally {
      setBulkBusy(false);
    }
  };

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (artists ?? [])
      .filter((a) => matches(a, filter))
      .filter((a) => !q || a.name.toLowerCase().includes(q));
  }, [artists, filter, query]);

  return (
    <>
      <PageHeader title="Music" />
      <div className="mx-auto w-full max-w-[1360px] px-4 py-6 sm:px-6">
        <LibraryToolbar
          count={artists === null ? null : artists.length}
          select={{ active: multiSelect, onToggle: () => (multiSelect ? exitSelect() : setMultiSelect(true)) }}
          tools={[{
            label: "Scan library",
            busyLabel: "Scanning…",
            busy: scan.running,
            title: "Find artists already in your music folder and catalog them",
            onClick: async () => {
              try {
                const r = await api.scanMusic();
                if (r.job_id) setScanJob(r.job_id);
                // The scan runs in the background — one MusicBrainz lookup per unknown
                // artist at a request per second — so say so rather than implying it's done.
                say("Scanning your music folder… this runs in the background.");
              } catch (e) {
                setError((e as Error).message);
              }
            },
          }]}
          primary={{ label: "+ Add artist", onClick: () => setAdding(true) }}
        />
        <FilterBar
          filters={FILTERS.map((f) => ({ key: f.key, label: f.label, count: (artists ?? []).filter((a) => matches(a, f.key)).length }))}
          active={filter}
          onFilter={(k) => setFilter(k as FilterKey)}
          query={query}
          onQuery={setQuery}
          noun="artists"
        />

        {error && (
          <div className="mb-4 rounded-lg p-3 text-[12px]" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>
            {error}
          </div>
        )}

        {multiSelect && (
          <div
            className="mb-3 flex flex-wrap items-center gap-2 rounded-xl px-3.5 py-2.5"
            style={{ background: "var(--panel)", border: "1px solid var(--accent-line, var(--line))" }}
          >
            <span className="text-[12.5px] font-semibold">
              {selected.size} selected
            </span>
            <button
              onClick={() => setSelected(new Set(shown.map((a) => a.id)))}
              className="rounded-lg px-2.5 py-1 text-[11.5px] font-semibold"
              style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
            >
              Select all ({shown.length})
            </button>
            <button
              onClick={clearSelect}
              disabled={selected.size === 0}
              className="rounded-lg px-2.5 py-1 text-[11.5px] font-semibold disabled:opacity-40"
              style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
            >
              Clear
            </button>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <button
                disabled={selected.size === 0 || bulkBusy}
                onClick={() => runBulk("Monitored", (id) => api.setArtistMonitored(id, true))}
                className="rounded-lg px-2.5 py-1 text-[11.5px] font-semibold disabled:opacity-40"
                style={{ border: "1px solid var(--accent)", color: "var(--accent)" }}
              >
                Monitor
              </button>
              <button
                disabled={selected.size === 0 || bulkBusy}
                onClick={() => runBulk("Unmonitored", (id) => api.setArtistMonitored(id, false))}
                className="rounded-lg px-2.5 py-1 text-[11.5px] font-semibold disabled:opacity-40"
                style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
              >
                Unmonitor
              </button>
              <select
                disabled={selected.size === 0 || bulkBusy || profiles.length === 0}
                value=""
                onChange={(e) => {
                  const v = e.target.value;
                  if (v) void runBulk("Set the profile on", (id) => api.setArtistProfile(id, v));
                  e.target.value = "";
                }}
                className="rounded-lg px-2 py-1 text-[11.5px] disabled:opacity-40"
                style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
              >
                <option value="">Quality profile…</option>
                {profiles.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name}
                  </option>
                ))}
              </select>
              <button
                disabled={selected.size === 0 || bulkBusy}
                onClick={async () => {
                  const ok = await confirm({
                    title: `Remove ${selected.size} artist${selected.size === 1 ? "" : "s"} from Arrmada?`,
                    body: "Files on disk are kept.",
                    confirmLabel: "Remove",
                    tone: "danger",
                  });
                  if (ok) void runBulk("Removed", (id) => api.deleteArtist(id));
                }}
                className="rounded-lg px-2.5 py-1 text-[11.5px] font-semibold disabled:opacity-40"
                style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}
              >
                Remove
              </button>
            </div>
          </div>
        )}

        {artists === null ? (
          <Skeleton variant="list" />
        ) : shown.length === 0 ? (
          <div className="rounded-xl p-10 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>
            {artists.length === 0 ? "No artists yet. Add one to get started." : "No artists match this filter."}
          </div>
        ) : (
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))" }}>
            {shown.map((a) => (
              <ArtistCard
                key={a.id}
                a={a}
                selectable={multiSelect}
                selected={selected.has(a.id)}
                onToggleSelect={() => toggleSelect(a.id)}
              />
            ))}
          </div>
        )}
      </div>

      {adding && (
        <AddArtistModal
          onClose={() => setAdding(false)}
          onAdded={(name) => {
            load();
            say(`Added ${name}.`);
          }}
        />
      )}
    </>
  );
}

function ArtistCard({
  a,
  selectable,
  selected,
  onToggleSelect,
}: {
  a: Artist;
  selectable: boolean;
  selected: boolean;
  onToggleSelect: () => void;
}) {
  const done = have(a);
  const all = total(a);
  const pct = all > 0 ? Math.round((done / all) * 100) : 0;
  const st = libraryStatus(statusInput(a));
  const tone = TONE_HUE[st.tone];

  const body = (
    <>
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-2">
          {selectable && (
            <span
              className="mt-0.5 grid h-4 w-4 flex-none place-items-center rounded text-[10px] font-bold"
              style={{
                border: `1px solid ${selected ? "var(--accent)" : "var(--line)"}`,
                background: selected ? "var(--accent)" : "transparent",
                color: "var(--accent-ink)",
              }}
            >
              {selected ? "✓" : ""}
            </span>
          )}
          <div className="min-w-0">
            <div className="truncate text-[13.5px] font-semibold">{a.name}</div>
            <div className="mt-0.5 truncate text-[11px] text-ink-faint">
              {a.stats?.albums ?? 0} album{(a.stats?.albums ?? 0) === 1 ? "" : "s"}
              {a.genres && a.genres.length > 0 && <> · {a.genres[0]}</>}
            </div>
          </div>
        </div>
        <StatusChip tone={st.tone} className="flex-none">
          {st.label}
        </StatusChip>
      </div>
      <div className="flex items-center gap-2">
        <div className="h-1.5 flex-1 overflow-hidden rounded-full" style={{ background: "var(--line)" }}>
          <div className="h-full rounded-full" style={{ width: `${pct}%`, background: tone }} />
        </div>
        <span className="flex-none font-mono text-[10.5px] text-ink-faint">
          {all > 0 ? `${done}/${all}` : "—"}
        </span>
      </div>
    </>
  );

  const cls = "flex flex-col gap-2 rounded-xl p-3.5 text-left transition-colors hover:bg-[var(--panel-2)]";
  const style = {
    background: selected ? "var(--accent-soft)" : "var(--panel)",
    border: `1px solid ${selected ? "var(--accent)" : "var(--line)"}`,
  };

  // In select mode the card stops being a link entirely — a card that both navigates and
  // selects depending on where you click is how you lose a selection by mis-clicking.
  if (selectable) {
    return (
      <button type="button" onClick={onToggleSelect} className={cls} style={style}>
        {body}
      </button>
    );
  }
  return (
    <Link to={`/music/${a.id}`} className={cls} style={style}>
      {body}
    </Link>
  );
}

// AddArtistModal searches MusicBrainz. The disambiguation is shown prominently because it's
// very often the only way to tell two same-named artists apart.
function AddArtistModal({ onClose, onAdded }: { onClose: () => void; onAdded: (name: string) => void }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<ArtistLookup[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const search = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!query.trim()) return;
    setSearching(true);
    setError(null);
    try {
      setResults(await api.lookupArtists(query.trim()));
    } catch (err) {
      setError((err as Error).message);
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  const add = async (r: ArtistLookup) => {
    setSaving(r.mbid);
    setError(null);
    try {
      await api.addArtist({ mbid: r.mbid, monitored: true });
      onAdded(r.name);
      onClose();
    } catch (err) {
      setError((err as Error).message);
      setSaving(null);
    }
  };

  return (
    <Modal onClose={onClose} labelledBy="add-artist-title" size="lg" initialFocus={inputRef} dismissible={saving === null}>
        <div className="mb-1 flex items-center justify-between">
          <h2 id="add-artist-title" className="m-0 text-[15px] font-bold">Add an artist</h2>
          <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">
            ✕
          </IconButton>
        </div>
        <p className="mb-3 text-[12px] text-ink-dim">
          Searches MusicBrainz. Adding an artist brings in their albums; track listings fill in when you open an album.
        </p>

        <form className="mb-3 flex gap-2" onSubmit={search}>
          <input
            ref={inputRef}
            aria-label="Artist name"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Artist name…"
            className="min-w-0 flex-1 rounded-lg px-3 py-2 text-[13px]"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
          />
          <Button type="submit" variant="primary" busy={searching} busyLabel="Searching…" className="disabled:opacity-50">
            Search
          </Button>
        </form>

        {error && (
          <div className="mb-3 rounded-lg p-2.5 text-[12px]" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>
            {error}
          </div>
        )}

        {results !== null && (
          results.length === 0 ? (
            <div className="rounded-xl p-6 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>
              No artists found.
            </div>
          ) : (
            <div className="thin-scroll flex max-h-[52vh] flex-col overflow-y-auto">
              {results.map((r) => (
                <div key={r.mbid} className="flex items-center gap-3 border-b py-2.5" style={{ borderColor: "var(--line)" }}>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">
                      {r.name}
                      {r.disambiguation && <span className="ml-2 text-[11.5px] text-ink-faint">({r.disambiguation})</span>}
                    </div>
                    <div className="truncate font-mono text-[10.5px] text-ink-faint">
                      {[r.type, r.country].filter(Boolean).join(" · ") || r.mbid}
                    </div>
                  </div>
                  <button
                    disabled={saving !== null}
                    onClick={() => add(r)}
                    className="flex-none rounded-lg px-3 py-1.5 text-[12px] font-semibold disabled:opacity-50"
                    style={{ border: "1px solid var(--accent)", color: "var(--accent)" }}
                  >
                    {saving === r.mbid ? "Adding…" : "Add"}
                  </button>
                </div>
              ))}
            </div>
          )
        )}
    </Modal>
  );
}
