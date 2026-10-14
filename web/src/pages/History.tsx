import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, type HistoryEvent, type ImportRecord } from "../lib/api";
import { categoryChip, EVENT_CATEGORIES, eventHref, eventKey, groupByDay, HISTORY_KINDS, mergeFirstPage } from "../lib/events";
import { formatBytes } from "../lib/format";
import { posterThumb } from "../lib/img";
import { useMe } from "../lib/me";
import { useLiveRefresh } from "../lib/useLiveQuery";
import { USENET_KEY_PREFIX } from "../lib/network";
import { UsenetChip } from "../components/UsenetChip";
import { Button, EmptyState, ErrorState, Skeleton, StaleBanner, StatusChip } from "../ui";

const PAGE_SIZE = 50;

// What the page refetches on, besides coming back to the window: something was grabbed,
// imported, held or removed. A burst (a sweep) makes one refetch.
const HISTORY_TOPICS = ["release.grabbed", "*.imported", "import.held", "file.removed"];

// The filters live in the address (?kind=series&event=imported&q=dune), so a filtered
// view can be linked, reloaded, and stepped back out of.
function useHistoryFilters() {
  const [params, setParams] = useSearchParams();
  const set = (key: string, value: string, replace = false) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    }, { replace });
  return {
    kind: params.get("kind") ?? "",
    event: params.get("event") ?? "",
    q: params.get("q") ?? "",
    files: params.get("view") === "imports",
    set,
  };
}

// HistoryPanel is Activity → History: a dated log of what happened to every title —
// grabs, imports, upgrades, failures, blocklists, removals and seed clean-ups — for
// movies, series, books and music, each row linking to its title. It doesn't poll: it
// refetches when the window comes back into focus and when the server announces a grab
// or an import.
export function HistoryPanel() {
  const f = useHistoryFilters();
  const { musicEnabled } = useMe();
  const [input, setInput] = useState(f.q);
  // Typing narrows after a pause; each keystroke doesn't become a request or a Back step.
  useEffect(() => {
    const t = window.setTimeout(() => { if (input.trim() !== f.q) f.set("q", input.trim(), true); }, 300);
    return () => window.clearTimeout(t);
  }, [input]); // eslint-disable-line react-hooks/exhaustive-deps

  const pill = (on: boolean) => ({
    border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`,
    background: on ? "var(--accent-soft)" : "var(--panel)",
    color: on ? "var(--accent-text)" : "var(--ink-faint)",
  });
  const kinds = HISTORY_KINDS.filter((k) => k.key !== "music" || musicEnabled);

  return (
    <div className="max-w-[1100px]">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div role="group" aria-label="Kind" className="flex flex-wrap items-center gap-1.5">
          {[{ key: "", label: "All" }, ...kinds].map((k) => (
            <button key={k.key || "all"} type="button" aria-pressed={f.kind === k.key} onClick={() => f.set("kind", k.key)} className="rounded-full px-3 py-1 text-sm font-semibold" style={pill(f.kind === k.key)}>
              {k.label}
            </button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <button type="button" aria-pressed={f.files} onClick={() => f.set("view", f.files ? "" : "imports")} title="The files imported most recently and where each went" className="rounded-full px-3 py-1 text-sm font-semibold" style={pill(f.files)}>
            Files imported
          </button>
          {!f.files && (
            <input value={input} onChange={(e) => setInput(e.target.value)} placeholder="Search titles or releases…" aria-label="Search the history"
              className="w-[220px] rounded-lg px-3 py-1.5 text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          )}
        </div>
      </div>
      {f.files ? <ImportedFiles kind={f.kind} /> : (
        <>
          <div role="group" aria-label="Event" className="mb-4 flex flex-wrap items-center gap-1.5">
            {[{ key: "", label: "Everything" }, ...EVENT_CATEGORIES].map((c) => (
              <button key={c.key || "all"} type="button" aria-pressed={f.event === c.key} onClick={() => f.set("event", c.key)} className="rounded-full px-2.5 py-0.5 text-sm font-semibold" style={pill(f.event === c.key)}>
                {c.label}
              </button>
            ))}
          </div>
          <EventLog kind={f.kind} event={f.event} q={f.q} />
        </>
      )}
    </div>
  );
}

function EventLog({ kind, event, q }: { kind: string; event: string; q: string }) {
  const [events, setEvents] = useState<HistoryEvent[] | null>(null);
  const [next, setNext] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [moreBusy, setMoreBusy] = useState(false);
  // The filters an answer was asked for; one for older filters is dropped.
  const asked = useRef("");
  const filterKey = `${kind}|${event}|${q}`;
  const loaded = useRef<HistoryEvent[]>([]);
  const loadedNext = useRef("");

  const load = useCallback(async (fresh: boolean) => {
    const key = `${kind}|${event}|${q}`;
    asked.current = key;
    setBusy(true);
    try {
      const page = await api.history({ kind, event, q, limit: PAGE_SIZE });
      if (asked.current !== key) return;
      const full = page.events.length === PAGE_SIZE;
      const merged = fresh ? null : mergeFirstPage(page.events, loaded.current, full);
      const list = merged ?? page.events;
      const nextBefore = merged && merged.length > page.events.length ? loadedNext.current : page.next_before;
      loaded.current = list;
      loadedNext.current = nextBefore;
      setEvents(list);
      setNext(nextBefore);
      setError(null);
    } catch (e) {
      if (asked.current === key) setError(e as Error);
    } finally {
      if (asked.current === key) setBusy(false);
    }
  }, [kind, event, q]);

  // A new filter starts the list over.
  useEffect(() => {
    loaded.current = [];
    loadedNext.current = "";
    setEvents(null);
    setNext("");
    void load(true);
  }, [filterKey]); // eslint-disable-line react-hooks/exhaustive-deps

  // Coming back to the window, and news of a grab or an import on the shared socket (a
  // burst makes one request), bring the newest in. No poll: a reconnect catches up.
  useEffect(() => {
    const onFocus = () => { void load(false); };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [load]);
  useLiveRefresh(() => load(false), { topics: HISTORY_TOPICS, fallbackMs: null, debounceMs: 1500 });

  const more = async () => {
    if (!next) return;
    setMoreBusy(true);
    const key = filterKey;
    try {
      const page = await api.history({ kind, event, q, before: next, limit: PAGE_SIZE });
      if (asked.current !== key) return;
      const seen = new Set(loaded.current.map(eventKey));
      const list = [...loaded.current, ...page.events.filter((e) => !seen.has(eventKey(e)))];
      loaded.current = list;
      loadedNext.current = page.next_before;
      setEvents(list);
      setNext(page.next_before);
    } catch (e) {
      setError(e as Error);
    } finally {
      setMoreBusy(false);
    }
  };

  if (events === null) {
    return error ? <ErrorState what="the history" message={error.message} onRetry={() => void load(true)} busy={busy} /> : <Skeleton variant="list" count={6} />;
  }
  const filtered = !!(kind || event || q);
  return (
    <>
      {error && <StaleBanner message={error.message} onRetry={() => void load(false)} />}
      {events.length === 0 ? (
        <EmptyState
          title={filtered ? "Nothing matches" : "Nothing has happened yet"}
          body={filtered ? "No events match these filters." : "Grabs, imports, upgrades and failures are logged here as they happen, for every title."}
        />
      ) : (
        <div className="flex flex-col gap-4">
          {groupByDay(events).map((day) => (
            <section key={day.label} aria-label={day.label}>
              <h2 className="mb-1.5 font-mono text-2xs font-bold uppercase tracking-[0.12em] text-ink-faint">{day.label}</h2>
              <ul className="m-0 list-none overflow-hidden rounded-xl p-0" style={{ border: "1px solid var(--line)" }}>
                {day.items.map((e) => <EventRow key={eventKey(e)} e={e} />)}
              </ul>
            </section>
          ))}
          {next && (
            <div className="flex justify-center">
              <Button size="sm" busy={moreBusy} busyLabel="Loading…" onClick={() => void more()}>Load more</Button>
            </div>
          )}
        </div>
      )}
    </>
  );
}

const KIND_WORD: Record<HistoryEvent["kind"], string> = { movie: "Movie", series: "TV", book: "Book", music: "Music" };

function EventRow({ e }: { e: HistoryEvent }) {
  const chip = categoryChip(e.category);
  const at = new Date(e.at_ms);
  const href = eventHref(e.kind, e.item_id);
  return (
    <li className="flex items-center gap-3 px-3 py-2" style={{ background: "var(--panel)", borderBottom: "1px solid var(--line-soft)" }}>
      <Link to={href} tabIndex={-1} aria-hidden="true" className="h-[42px] w-[28px] flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
        {e.poster_url && <img src={posterThumb(e.poster_url)} alt="" loading="lazy" className="h-full w-full object-cover" />}
      </Link>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
          <StatusChip tone={chip.tone}>{chip.label}</StatusChip>
          <Link to={href} className="min-w-0 truncate text-base font-semibold no-underline" style={{ color: "var(--ink)" }}>{e.title}</Link>
          <span className="font-mono text-2xs uppercase text-ink-faint">{KIND_WORD[e.kind]}</span>
          {e.network === "usenet" && <UsenetChip />}
        </div>
        {e.detail && <div className="mt-0.5 truncate text-sm text-ink-dim" title={e.detail}>{e.detail}</div>}
      </div>
      <time dateTime={at.toISOString()} title={at.toLocaleString()} className="flex-none font-mono text-xs text-ink-faint">
        {at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
      </time>
    </li>
  );
}

// ImportedFiles is the old History: the files imported most recently, where each went,
// how big, and when. Kept for one release beside the event log.
function ImportedFiles({ kind }: { kind: string }) {
  const [items, setItems] = useState<ImportRecord[] | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const load = useCallback(() => api.importedFiles().then((xs) => { setItems(xs); setError(null); }).catch((e: Error) => setError(e)), []);
  useEffect(() => { void load(); }, [load]);
  if (items === null) return error ? <ErrorState what="imported files" message={error.message} onRetry={() => void load()} /> : <Skeleton variant="list" count={6} />;
  if (items.length === 0) return <EmptyState title="Nothing imported yet" body="When a download finishes, Arrmada files it into your library and it's listed here." />;
  return (
    <div className="flex flex-col gap-2">
      {kind && <p className="m-0 text-sm text-ink-faint">Imported files aren't split by kind; this list has every one.</p>}
      {items.map((it) => {
        const at = it.imported_at ? new Date(it.imported_at.includes("T") ? it.imported_at : it.imported_at.replace(" ", "T") + "Z") : null;
        return (
          <div key={it.hash} className="rounded-xl px-4 py-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
            <div className="flex items-center gap-3">
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-md font-semibold">{it.title || "Imported"}</span>
                  {/* A usenet job's download key is "sab-…" (a torrent's is its hash). */}
                  {it.hash.toLowerCase().startsWith(USENET_KEY_PREFIX) && <UsenetChip className="flex-none" />}
                </div>
                <div className="truncate font-mono text-xs text-ink-faint" title={it.target_path}>{it.target_path}</div>
              </div>
              <span className="font-mono text-xs text-ink-dim">{formatBytes(it.size_bytes)}</span>
              {at && !Number.isNaN(at.getTime()) && (
                <time dateTime={at.toISOString()} className="w-[120px] flex-none text-right font-mono text-xs text-ink-faint">{at.toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}</time>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
