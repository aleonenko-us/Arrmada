import { useCallback, useEffect, useRef, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { type LogEntry } from "../lib/api";
import { systemApi, type LogLevelState } from "../lib/systemApi";
import { usePoll } from "../lib/usePoll";
import { Button, Menu, useToast } from "../ui";

const LEVELS = ["debug", "info", "warn", "error"] as const;
const LEVEL_STYLE: Record<string, { color: string; label: string }> = {
  DEBUG: { color: "var(--ink-faint)", label: "DBG" },
  INFO: { color: "var(--accent)", label: "INF" },
  WARN: { color: "var(--avoid)", label: "WRN" },
  ERROR: { color: "var(--reject)", label: "ERR" },
};

// The routine chatter that drowns everything else. Indexer tracing logs a line per page
// per indexer per query, so a handful of searches buries every import and grab in the
// buffer. One click to drop it.
const NOISE = "torznab page,indexer search";

// How many lines the page asks for. It is only the viewer's window: the server keeps far
// more in memory and on disk, which the footer says.
const LOG_LIMIT = 2000;
// Live mode appends what's new; past this many lines the oldest drop off the view.
const CLIENT_MAX = 5000;

// Saves a server download (the session cookie authenticates it) without leaving the page.
function saveURL(url: string) {
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  a.click();
}

// The debug button's length; the server allows up to its max_minutes.
const DEBUG_MINUTES = 30;

function fmtTime(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// Full timestamp for the hover title — HH:MM:SS alone can't tell you which day a line
// is from, which matters when the buffer spans an overnight run.
function fmtFull(ms: number): string {
  return new Date(ms).toLocaleString();
}

function asText(entries: LogEntry[]): string {
  return entries.map((e) => `${fmtFull(e.time_ms)} ${e.level} ${e.module ? `[${e.module}] ` : ""}${e.msg}${e.attrs ? " " + e.attrs : ""}`).join("\n");
}

function fmtLeft(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

// DebugSwitch raises the server's log level to debug for a while. The server turns it off
// by itself when the time is up (and a restart always starts at the install's level), so
// the countdown here is only a display of the server's deadline.
function DebugSwitch({ onEnabled }: { onEnabled: () => void }) {
  const toast = useToast();
  const [state, setState] = useState<LogLevelState | null>(null);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback(() => systemApi.logLevel().then(setState).catch(() => setState(null)), []);
  useEffect(() => { load(); }, [load]);

  const until = state?.until ? Date.parse(state.until) : 0;
  const raised = !!state && state.level !== state.env_level && until > 0;
  // Tick once a second while raised; at zero, ask the server, which has reverted by then.
  usePoll(() => {
    setNow(Date.now());
    if (until && Date.now() >= until) return load();
  }, raised ? 1000 : null, { immediate: false });

  const change = async (level: string) => {
    setBusy(true);
    try {
      const next = await systemApi.setLogLevel(level, level ? DEBUG_MINUTES : undefined);
      setState(next);
      setNow(Date.now());
      if (level) onEnabled();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };

  if (!state) return null;
  if (state.env_level === "debug") {
    return <span className="text-[11.5px] text-ink-faint" title="ARRMADA_LOG_LEVEL is debug for this install">Debug is always on</span>;
  }
  if (raised) {
    return (
      <span className="flex items-center gap-2">
        <span className="font-mono text-[11.5px]" style={{ color: "var(--accent)" }} title={`Debug lines until ${new Date(until).toLocaleTimeString()}, then back to ${state.env_level}`}>
          Debug on · {fmtLeft(until - now)} left
        </span>
        <Button size="sm" onClick={() => change("")} busy={busy}>Back to normal</Button>
      </span>
    );
  }
  return (
    <Button size="sm" onClick={() => change("debug")} busy={busy} title={`Log debug lines for ${DEBUG_MINUTES} minutes, then go back to ${state.env_level} by itself`}>
      Debug for {DEBUG_MINUTES} min
    </Button>
  );
}

// Logs — a live view of Arrmada's application log (also on stdout/docker logs). Filter by
// level and search text; auto-refreshes so you can watch things happen.
export function Logs() {
  const [entries, setEntries] = useState<LogEntry[] | null>(null);
  const [capacity, setCapacity] = useState<number | null>(null);
  const [modules, setModules] = useState<string[]>([]);
  const [level, setLevel] = useState("info");
  const [module, setModule] = useState("");
  const [q, setQ] = useState("");
  const [hide, setHide] = useState("");
  const [auto, setAuto] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);

  // Typing fired a request per keystroke, each pulling up to 2000 entries. Settle first.
  const [dq, setDq] = useState("");
  const [dhide, setDhide] = useState("");
  useEffect(() => {
    const t = window.setTimeout(() => { setDq(q); setDhide(hide); }, 250);
    return () => window.clearTimeout(t);
  }, [q, hide]);

  // cursor is the newest line the server had at the last answer: Live mode asks only for
  // what came after it. gen ties an answer to the filters it was asked with, so a poll
  // that was in flight when the filters changed can't append lines from the old ones.
  const cursor = useRef(0);
  const gen = useRef(0);
  const run = useRef<number | undefined>(undefined);
  const filters = useCallback(() => ({ level, q: dq, hide: dhide, module }), [level, dq, dhide, module]);

  // A full load: the newest window for these filters.
  const loading = useRef(false);
  const load = useCallback(() => {
    const g = ++gen.current;
    loading.current = true;
    return systemApi.logs({ limit: LOG_LIMIT, ...filters() })
      .finally(() => { if (g === gen.current) loading.current = false; })
      .then((r) => {
        if (g !== gen.current) return;
        setEntries(r.entries);
        cursor.current = r.last_seq ?? 0;
        run.current = r.run;
        setCapacity(r.capacity ?? null);
        setModules(r.modules ?? []);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, [filters]);

  // A Live tick: only the lines after the cursor, appended and trimmed to CLIENT_MAX. A
  // reset answer, or a new run, means the server restarted: the view starts over.
  const tick = useCallback(() => {
    if (loading.current) return; // the full load under way brings everything
    if (!cursor.current) return load();
    const g = gen.current;
    return systemApi.logs({ limit: LOG_LIMIT, ...filters(), after_seq: cursor.current })
      .then((r) => {
        if (g !== gen.current) return;
        if (r.run !== run.current) return load(); // restarted: the old cursor means nothing now
        cursor.current = r.last_seq ?? cursor.current;
        if (r.reset) {
          setEntries(r.entries);
        } else if (r.entries.length) {
          setEntries((prev) => {
            const next = [...(prev ?? []), ...r.entries];
            return next.length > CLIENT_MAX ? next.slice(next.length - CLIENT_MAX) : next;
          });
        }
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, [filters, load]);

  useEffect(() => { load(); }, [load]);
  usePoll(tick, auto ? 3000 : null, { immediate: false });

  // Keep the view pinned to the bottom (newest) unless the user scrolls up.
  useEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [entries]);
  const onScroll = () => {
    const el = scroller.current;
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  const [copied, setCopied] = useState(false);
  const copyAll = async () => {
    try {
      await navigator.clipboard?.writeText(asText(entries ?? []));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setErr("Couldn't copy — the browser blocked clipboard access. Use Download instead.");
    }
  };

  // Copy is fine for a handful of lines; a few thousand is a file. Saves the whole
  // filtered view with full timestamps, which is what you want when sharing a problem.
  const download = () => {
    const blob = new Blob([asText(entries ?? [])], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
    a.href = url;
    a.download = `arrmada-logs-${stamp}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const pill = (on: boolean) => ({ border: `1px solid ${on ? "var(--accent-line)" : "var(--line)"}`, color: on ? "var(--accent)" : "var(--ink-dim)", background: "transparent" });

  return (
    <>
      <PageHeader title="Logs" />
      <div className="mx-auto flex max-w-[1100px] flex-col gap-3 px-4 py-5">
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex overflow-hidden rounded-lg" role="group" aria-label="Lowest level shown" style={{ border: "1px solid var(--line)" }}>
            {LEVELS.map((l) => (
              <button key={l} onClick={() => setLevel(l)} aria-pressed={level === l} className="px-3 py-1.5 text-[11.5px] font-semibold capitalize" style={{ background: level === l ? "var(--accent-soft)" : "transparent", color: level === l ? "var(--accent)" : "var(--ink-dim)" }}>{l}</button>
            ))}
          </div>
          <input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Show only lines containing" placeholder="Show only lines containing…" className="min-w-[200px] flex-1 rounded-lg px-3 py-1.5 text-[12.5px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          <input value={hide} onChange={(e) => setHide(e.target.value)} aria-label="Hide lines containing" placeholder="Hide lines containing… (comma-separated)" title="Drop noisy lines. Comma-separated; matches the message or any field." className="min-w-[200px] flex-1 rounded-lg px-3 py-1.5 text-[12.5px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
          <Button
            size="sm"
            onClick={() => setHide((h) => (h === NOISE ? "" : NOISE))}
            aria-pressed={hide === NOISE}
            title="Hide per-page indexer tracing, which logs a line per page per indexer per query and buries everything else"
            style={pill(hide === NOISE)}
          >
            {hide === NOISE ? "Noise hidden" : "Hide noise"}
          </Button>
          <select value={module} onChange={(e) => setModule(e.target.value)} aria-label="Module" title="Only one part of the app's lines" className="rounded-lg px-2.5 py-1.5 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
            <option value="">All modules</option>
            {modules.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
          <Button size="sm" onClick={() => setAuto((v) => !v)} aria-pressed={auto} style={pill(auto)}>{auto ? "Live ●" : "Paused"}</Button>
          <Button size="sm" onClick={load}>Refresh</Button>
          <Button size="sm" onClick={copyAll}>{copied ? "Copied ✓" : "Copy"}</Button>
          <Menu
            trigger="Download ▾"
            label="Download logs"
            triggerClassName="rounded-md px-2.5 py-1 text-[11.5px] font-semibold"
            triggerStyle={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            menuWidthClassName="min-w-[260px]"
            items={[
              { label: "This view (.txt)", title: "The lines shown here, with full timestamps", onSelect: download },
              { label: "Full log history (.zip)", title: "Every line on disk, including the rotated files. For you: it names your library's titles.", onSelect: () => saveURL("/api/v1/logs/download") },
              { label: "Support bundle (.zip)", title: "Logs, status, health and safe settings with credentials, people and book names taken out — made to be shared", onSelect: () => saveURL("/api/v1/system/support-bundle") },
            ]}
          />
          <DebugSwitch onEnabled={() => setLevel("debug")} />
        </div>

        {err && <div className="text-[12px]" style={{ color: "var(--reject)" }}>{err}</div>}

        <div ref={scroller} onScroll={onScroll} className="thin-scroll overflow-auto rounded-xl p-2 font-mono text-[11.5px] leading-[1.5]" style={{ background: "var(--panel)", border: "1px solid var(--line)", height: "calc(100vh - 220px)" }}>
          {entries === null ? (
            <div className="p-6 text-center text-ink-dim">Loading…</div>
          ) : entries.length === 0 ? (
            <div className="p-6 text-center text-ink-dim">
              No log lines match.
              {(dq || dhide || module) && <div className="mt-1 text-[11px]">Your filters may be too narrow — clear them to see everything.</div>}
            </div>
          ) : (
            entries.map((e, i) => {
              const ls = LEVEL_STYLE[e.level] ?? LEVEL_STYLE.INFO;
              return (
                <div key={e.seq ?? `${e.time_ms}-${i}`} className="flex gap-2 whitespace-pre-wrap px-1 py-0.5" style={{ borderTop: i === 0 ? "none" : "1px solid var(--line-soft)" }}>
                  <span className="flex-none text-ink-faint" title={fmtFull(e.time_ms)}>{fmtTime(e.time_ms)}</span>
                  <span className="w-8 flex-none font-bold" style={{ color: ls.color }}>{ls.label}</span>
                  <span className="min-w-0 flex-1 break-words">
                    {e.module && !module && <span className="text-ink-faint">[{e.module}] </span>}<span style={{ color: "var(--ink)" }}>{e.msg}</span>
                    {e.attrs && <span className="text-ink-faint">  {e.attrs}</span>}
                  </span>
                </div>
              );
            })
          )}
        </div>
        <p className="text-[11px] text-ink-faint">
          Showing the newest {(entries?.length ?? 0).toLocaleString()} matching lines{dhide ? " (some hidden by your filter)" : ""}
          {capacity ? ` of the last ${capacity.toLocaleString()} kept in memory` : ""}. Older lines are in the logs folder on disk (/data/logs in the container); Download → Full log history has them all. Same stream as the container logs.
        </p>
      </div>
    </>
  );
}
