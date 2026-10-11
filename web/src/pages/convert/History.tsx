import { useEffect, useState, type ReactNode } from "react";
import { api, type ConvertHistoryEntry, type ConvertHistoryOutcome, type ConvertTrackDecision } from "../../lib/api";
import { fmtBytes } from "../../lib/disposal";
import { posterThumb } from "../../lib/img";
import { ReleaseButton, RevertButton, holdLine } from "./hold";

// Convert's History tab and the Overview's Recent card, both read from the ledger
// (GET /convert/history) — so they survive a restart, unlike the in-memory job list they
// replace. Every job's outcome is there; converted files stay listed for good, the rest for
// 90 days. Held conversions can be reverted or released from here.

const card = "rounded-xl p-4";
const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;
const lbl = "font-mono text-[9.5px] font-bold uppercase tracking-[0.11em] text-ink-faint";
const inp = "rounded-lg px-2.5 py-1.5 text-[12px]";
const inpStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;
const ghostBtn = { border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" } as const;

const OUTCOME_LABEL: Record<ConvertHistoryOutcome, string> = {
  done: "converted", skipped: "kept original", failed: "failed", cancelled: "cancelled", in_progress: "running",
};
const OUTCOME_TONE: Record<ConvertHistoryOutcome, string> = {
  done: "var(--good)", skipped: "var(--avoid)", failed: "var(--reject)", cancelled: "var(--ink-faint)", in_progress: "var(--accent)",
};
const OUTCOME_FILTERS: { key: ConvertHistoryOutcome | ""; label: string }[] = [
  { key: "", label: "All" }, { key: "done", label: "Converted" }, { key: "skipped", label: "Kept original" },
  { key: "failed", label: "Failed" }, { key: "cancelled", label: "Cancelled" },
];
const MEDIA_FILTERS: { key: "" | "movie" | "episode"; label: string }[] = [
  { key: "", label: "Movies & TV" }, { key: "movie", label: "Movies" }, { key: "episode", label: "TV" },
];

function fmtWhen(unix?: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function fmtDuration(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return "";
  const m = Math.round(sec / 60);
  return m < 60 ? `${Math.max(1, m)}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

// cropLabel turns the recorded ffmpeg crop ("crop=3840:1600:0:280") into "bars removed → 3840×1600".
function cropLabel(crop?: string): string {
  const m = /^crop=(\d+):(\d+):/.exec(crop ?? "");
  return m ? `bars removed → ${m[1]}×${m[2]}` : "";
}

// shortSpec is the before/after line's form of a stored spec: codec · resolution · HDR.
function shortSpec(spec?: string): string {
  return (spec ?? "").split(" · ").slice(0, 3).join(" · ");
}

function savedPct(e: ConvertHistoryEntry): number | null {
  return e.outcome === "done" && e.src_size > 0 && e.out_size > 0 ? Math.round((1 - e.out_size / e.src_size) * 100) : null;
}

function Pill({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-pressed={active} className="rounded-full px-2.5 py-1 text-[10.5px] font-semibold transition-colors"
      style={{ border: `1px solid ${active ? "var(--accent)" : "var(--line)"}`, background: active ? "var(--accent-soft)" : "transparent", color: active ? "var(--accent)" : "var(--ink-dim)" }}>
      {children}
    </button>
  );
}

function Poster({ e }: { e: ConvertHistoryEntry }) {
  const [broken, setBroken] = useState(false);
  const src = posterThumb(e.poster_url);
  const box = "h-[48px] w-[32px] flex-none overflow-hidden rounded";
  if (src && !broken) {
    return <img src={src} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} className={`${box} object-cover`} style={{ background: "var(--panel-2)" }} />;
  }
  return (
    <div aria-hidden className={`${box} flex items-center justify-center font-mono text-[10px] font-bold`} style={{ background: "var(--panel-2)", color: "var(--ink-faint)", border: "1px solid var(--line-soft)" }}>
      {e.kind === "episode" ? "TV" : "M"}
    </div>
  );
}

// HistoryTab is the whole ledger: filters, newest first, Load more.
export function HistoryTab({ reload, flash }: { reload: number; flash: (m: string) => void }) {
  const [outcome, setOutcome] = useState<ConvertHistoryOutcome | "">("");
  const [media, setMedia] = useState<"" | "movie" | "episode">("");
  const [q, setQ] = useState("");
  const [items, setItems] = useState<ConvertHistoryEntry[] | null>(null);
  const [next, setNext] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const filter = { outcome: outcome || undefined, media: media || undefined, q: q.trim() || undefined, limit: 30 };
  useEffect(() => {
    let alive = true;
    const t = window.setTimeout(() => {
      api.convertHistory({ outcome: outcome || undefined, media: media || undefined, q: q.trim() || undefined, limit: 30 })
        .then((r) => { if (alive) { setItems(r.items); setNext(r.next); } })
        .catch(() => { if (alive) { setItems([]); setNext(""); } });
    }, q ? 250 : 0);
    return () => { alive = false; window.clearTimeout(t); };
  }, [outcome, media, q, reload]);
  const more = async () => {
    setBusy(true);
    try {
      const r = await api.convertHistory({ ...filter, before: next });
      // A row can't repeat across pages (the cursor is strict), but a reload racing Load
      // more could: keep the first copy.
      setItems((cur) => {
        const seen = new Set((cur ?? []).map((x) => x.id));
        return [...(cur ?? []), ...r.items.filter((x) => !seen.has(x.id))];
      });
      setNext(r.next);
    } catch { /* the button stays for another try */ } finally { setBusy(false); }
  };
  const replace = (e: ConvertHistoryEntry) => setItems((cur) => (cur ?? []).map((x) => (x.id === e.id ? { ...x, ...e } : x)));

  return (
    <div className={card} style={cardStyle}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-[14px] font-bold">History</div>
          <p className="mt-0.5 text-[11.5px] text-ink-faint">Everything Convert did, and what came of it. Converted files stay listed; the rest for 90 days. While an original is held, its conversion can be reverted.</p>
        </div>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search titles" aria-label="Search titles" className={`${inp} w-[180px]`} style={inpStyle} />
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        {OUTCOME_FILTERS.map((f) => <Pill key={f.key || "all"} active={outcome === f.key} onClick={() => setOutcome(f.key)}>{f.label}</Pill>)}
        <span className="mx-1 h-4 w-px" style={{ background: "var(--line)" }} aria-hidden />
        {MEDIA_FILTERS.map((f) => <Pill key={f.key || "both"} active={media === f.key} onClick={() => setMedia(f.key)}>{f.label}</Pill>)}
      </div>
      <div className="mt-3 flex flex-col">
        {items === null ? <div className="text-[12px] text-ink-faint">Loading…</div>
          : items.length === 0 ? <div className="text-[12px] text-ink-faint">Nothing recorded yet{outcome || media || q ? " that matches" : ""}.</div>
          : items.map((e) => <HistoryRow key={e.id} e={e} open={open === e.id} onToggle={() => setOpen(open === e.id ? null : e.id)} flash={flash} onChanged={replace} />)}
      </div>
      {next && (
        <button onClick={more} disabled={busy} className="mt-2.5 rounded-lg px-3 py-1.5 text-[11.5px] font-semibold disabled:opacity-50" style={ghostBtn}>
          {busy ? "…" : "Load more"}
        </button>
      )}
    </div>
  );
}

function track(d: ConvertTrackDecision): string {
  return [d.type === "audio" ? "Audio" : d.image ? "Image subtitle" : "Subtitle", d.lang || "und", d.title, d.codec, d.channels ? `${d.channels} ch` : "", d.forced ? "forced" : ""]
    .filter(Boolean).join(" · ");
}

function HistoryRow({ e, open, onToggle, flash, onChanged }: {
  e: ConvertHistoryEntry; open: boolean; onToggle: () => void; flash: (m: string) => void; onChanged: (e: ConvertHistoryEntry) => void;
}) {
  const pct = savedPct(e);
  const windows = e.ssim_windows ?? [];
  const hold = holdLine(e);
  const dropped = e.dropped_tracks ?? [];
  return (
    <div className="border-t py-2 first:border-t-0" style={{ borderColor: "var(--line-soft)" }}>
      <div className="flex items-start gap-2.5">
        <Poster e={e} />
        <div className="min-w-0 flex-1">
          <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full flex-wrap items-center gap-x-2.5 gap-y-1 text-left text-[12px]">
            <span className="rounded px-1.5 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: "var(--panel-2)", color: OUTCOME_TONE[e.outcome] }}>{OUTCOME_LABEL[e.outcome]}</span>
            <span className="min-w-0 flex-1 truncate font-semibold">{e.title}</span>
            {(e.repeats ?? 1) > 1 && <span className="font-mono text-[10px] text-ink-faint" title={`The same ${e.repeats} times in a row`}>×{e.repeats}</span>}
            <span className="flex-none font-mono text-[10px] text-ink-faint">{fmtWhen(e.finished_at)}</span>
          </button>
          <div className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink-dim">
            {pct !== null ? (
              <span className="font-mono">
                {shortSpec(e.src_spec) || "original"} · {fmtBytes(e.src_size)} → {shortSpec(e.out_spec) || e.codec?.toUpperCase()} · {fmtBytes(e.out_size)} <span style={{ color: "var(--good)" }}>−{pct}%</span>
              </span>
            ) : e.note ? <span className="truncate" title={e.note}>{e.note}</span> : null}
            {e.ssim_mean ? <span className="font-mono">SSIM {e.ssim_mean.toFixed(3)}{e.ssim_min ? ` · worst ${e.ssim_min.toFixed(3)}` : ""}</span> : null}
            {dropped.length > 0 && <span title={dropped.map(track).join("\n")}>removed {dropped.length} track{dropped.length === 1 ? "" : "s"}</span>}
            {hold && <span style={{ color: e.hold_state === "held" ? "var(--accent)" : "var(--ink-faint)" }}>{hold}</span>}
          </div>
          {e.hold_state === "held" && e.outcome === "done" && (
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              <RevertButton e={e} flash={flash} onDone={onChanged} />
              <ReleaseButton e={e} flash={flash} onDone={onChanged} />
            </div>
          )}
        </div>
      </div>
      {open && (
        <div className="mt-2 flex flex-col gap-1.5 rounded-lg p-3 text-[11.5px] text-ink-dim" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
          {e.note && <div>{e.note}</div>}
          {e.src_spec && <div><span className={lbl}>Before</span> <span className="font-mono">{e.src_spec} · {fmtBytes(e.src_size)}</span></div>}
          {e.out_spec && <div><span className={lbl}>After</span> <span className="font-mono">{e.out_spec} · {fmtBytes(e.out_size)}</span></div>}
          {e.src_release && <div><span className={lbl}>Release</span> <span className="font-mono">{e.src_release}</span></div>}
          {(e.codec || e.encoder) && (
            <div><span className={lbl}>Encode</span> <span className="font-mono">
              {[e.codec?.toUpperCase(), e.crf ? `CRF ${e.crf}` : "", e.encoder, cropLabel(e.crop),
                e.encode_secs ? `${fmtDuration(e.encode_secs)} encoding` : ""].filter(Boolean).join(" · ")}
            </span></div>
          )}
          {windows.length > 0 && (
            <div><span className={lbl}>Quality</span> <span className="font-mono">
              SSIM {e.ssim_mean?.toFixed(4)} average, {e.ssim_min?.toFixed(4)} lowest · scenes {windows.map((w) => w.toFixed(3)).join(" ")}
            </span></div>
          )}
          {dropped.length > 0 && (
            <div><span className={lbl}>Removed</span>
              {dropped.map((d) => <div key={`${d.type}-${d.index}`} className="ml-2">{track(d)} <span className="text-ink-faint">— {d.reason}</span></div>)}
            </div>
          )}
          {(e.warnings ?? []).map((w) => <div key={w} style={{ color: "var(--avoid)" }}>{w}</div>)}
          {e.reclaim_deferred && <div className="text-ink-faint">Releasing the original won't free its space — the download client still holds a hardlinked copy of it.</div>}
          {hold && <div><span className={lbl}>Original</span> {hold}{e.hold_state === "held" && e.hold_until ? ` (until ${fmtWhen(e.hold_until)})` : ""}</div>}
          {e.out_path && <div className="truncate font-mono text-[10.5px] text-ink-faint" title={e.out_path}>{e.out_path}</div>}
        </div>
      )}
    </div>
  );
}

// RecentCard is the Overview's last few ledger rows, with a way into History.
export function RecentCard({ reload, onSeeAll }: { reload: number; onSeeAll: () => void }) {
  const [items, setItems] = useState<ConvertHistoryEntry[] | null>(null);
  useEffect(() => {
    let alive = true;
    api.convertHistory({ limit: 8 }).then((r) => { if (alive) setItems(r.items); }).catch(() => { if (alive) setItems([]); });
    return () => { alive = false; };
  }, [reload]);
  if (!items || items.length === 0) return null;
  return (
    <div className={card} style={cardStyle}>
      <div className="flex items-center justify-between">
        <div className="text-[14px] font-bold">Recent</div>
        <button onClick={onSeeAll} className="text-[11.5px] font-semibold" style={{ color: "var(--accent)" }}>See all in History →</button>
      </div>
      <div className="mt-2 flex flex-col gap-1.5">
        {items.map((e) => {
          const pct = savedPct(e);
          return (
            <div key={e.id} className="flex items-center gap-2.5 text-[12px]">
              <span className="rounded px-1.5 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: "var(--panel-2)", color: OUTCOME_TONE[e.outcome] }}>{OUTCOME_LABEL[e.outcome]}</span>
              <span className="min-w-0 flex-1 truncate font-semibold">{e.title}</span>
              {pct !== null ? (
                <span className="font-mono text-ink-dim">
                  {fmtBytes(e.src_size)} → {fmtBytes(e.out_size)} <span style={{ color: "var(--good)" }}>−{pct}%</span>
                  <span className="ml-1.5 text-[10.5px] text-ink-faint">{e.codec?.toUpperCase()}{e.ssim_mean ? ` · SSIM ${e.ssim_mean.toFixed(3)}` : ""}</span>
                </span>
              ) : (
                <span className="max-w-[55%] truncate text-[11.5px] text-ink-faint" title={e.note}>{e.note || OUTCOME_LABEL[e.outcome]}</span>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
