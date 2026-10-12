import { useEffect, useState } from "react";
import type { FormatCheck, ParsedRelease, QualityExplain, StoredProfile } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";

// TestName is the builder's "Test a release name" card: paste a name and see what Arrmada
// reads from it and what this profile — as edited, saved or not — makes of it. It answers
// "why does this release never match my format?" and "why is it rejected?" without a search.

const panelStyle = { background: "var(--panel)", border: "1px solid var(--line)" };
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

export function TestName({ sp }: { sp: StoredProfile }) {
  const [name, setName] = useState("");
  const [size, setSize] = useState("");
  const [runtime, setRuntime] = useState("");
  const [res, setRes] = useState<QualityExplain | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const trimmed = name.trim();

  // Re-judged on every edit to the name or the profile, debounced like the preview.
  useEffect(() => {
    if (!trimmed) { setRes(null); setErr(null); return; }
    let alive = true;
    const t = window.setTimeout(() => {
      qualityApi.qualityExplain({ profile: sp, name: trimmed, size_gb: Number(size) || 0, runtime_min: Math.round(Number(runtime) || 0) })
        .then((r) => { if (alive) { setRes(r); setErr(null); } })
        .catch((e) => { if (alive) setErr((e as Error).message); });
    }, 250);
    return () => { alive = false; window.clearTimeout(t); };
  }, [sp, trimmed, size, runtime]);

  return (
    <div className="rounded-xl p-4" style={panelStyle}>
      <div className="mb-0.5 text-[13px] font-semibold">Test a release name</div>
      <div className="mb-2.5 text-[11px] text-ink-faint">Paste a name to see what's read from it and what this profile, as edited, makes of it.</div>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Show.S01E01.1080p.WEB.h264-GROUP" aria-label="Release name to test"
        className="w-full rounded-lg px-2.5 py-1.5 font-mono text-[12px]" style={fieldStyle} />
      <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-ink-faint">
        <label className="flex items-center gap-1.5">
          Size
          <input type="number" min={0} step={0.1} value={size} onChange={(e) => setSize(e.target.value)} placeholder="—" aria-label="Size in GB"
            className="w-[64px] rounded-lg px-2 py-1 text-right font-mono text-[12px]" style={fieldStyle} />
          GB
        </label>
        <label className="flex items-center gap-1.5">
          Runtime
          <input type="number" min={0} step={1} value={runtime} onChange={(e) => setRuntime(e.target.value)} placeholder="—" aria-label="Runtime in minutes"
            className="w-[64px] rounded-lg px-2 py-1 text-right font-mono text-[12px]" style={fieldStyle} />
          min
        </label>
        <span className="text-[10.5px]">Both, and the bitrate window applies.</span>
      </div>
      {err && <div className="mt-2.5 text-[11.5px]" style={{ color: "var(--reject)" }}>{err}</div>}
      {res && trimmed && <Result res={res} />}
    </div>
  );
}

function Result({ res }: { res: QualityExplain }) {
  const ev = res.evaluation;
  const r = res.release;
  // GiB over minutes in Mb/s — the same sum the server's bitrate window uses.
  const bitrate = ev.candidate.runtime_min && ev.candidate.size_gb
    ? (ev.candidate.size_gb * 1024 ** 3 * 8) / 1e6 / (ev.candidate.runtime_min * 60) : 0;
  return (
    <div className="mt-3 flex flex-col gap-2.5">
      <ParsedChips r={r} bitrate={bitrate} />
      <Verdict ev={ev} />
      <FormatTable formats={res.formats ?? []} />
    </div>
  );
}

// ParsedChips lists what the parser read, with an "inferred" badge where the name didn't
// say it outright.
function ParsedChips({ r, bitrate }: { r: ParsedRelease; bitrate: number }) {
  const chips: { label: string; value: string; inferred?: boolean }[] = [];
  const add = (label: string, value: string | undefined, inferred?: boolean) => { if (value) chips.push({ label, value, inferred }); };
  add("Resolution", r.resolution, r.resolution_inferred);
  add("Source", r.source, r.source_inferred);
  add("Codec", r.codec);
  add("HDR", (r.hdr ?? []).join(" + "));
  add("Audio", (r.audio ?? []).join(" + "));
  add("Group", r.group);
  add("Edition", r.edition);
  add("Streaming", r.streaming);
  add("Languages", [...(r.languages ?? []), ...(r.multi ? ["multi"] : [])].join(", "));
  const flags = [r.proper && "PROPER", r.repack && "REPACK", r.full_disc && "Full disc", r.three_d && "3D", r.upscaled && "Upscaled", r.extras && "Extras"].filter(Boolean) as string[];
  add("Flags", flags.join(" · "));
  if (bitrate > 0) add("Bitrate", `${bitrate.toFixed(1)} Mb/s`);
  return (
    <div>
      <div className="mb-1.5 font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">Read from the name</div>
      {chips.length === 0 ? (
        <div className="text-[11.5px] text-ink-dim">Nothing recognisable — no resolution, source or codec in the name.</div>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {chips.map((c) => (
            <span key={c.label} className="inline-flex items-center gap-1 rounded-md px-1.5 py-1 font-mono text-[10.5px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line-soft)", color: "var(--ink-dim)" }}>
              <span className="text-ink-faint">{c.label}</span> {c.value}
              {c.inferred && <span className="rounded px-1 text-[8.5px] font-bold uppercase" style={{ background: "var(--line)", color: "var(--ink-dim)" }} title="Not written in the name — read from how it's written">inferred</span>}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// Verdict is the one-line answer: grabbable, grabbable but avoided, or refused and why.
function Verdict({ ev }: { ev: QualityExplain["evaluation"] }) {
  let tone = "var(--good)", bg = "var(--good-soft)", text = "Eligible — this profile would grab it";
  if (!ev.eligible) {
    tone = "var(--reject)"; bg = "var(--reject-soft)"; text = ev.reject_reason || "Not eligible";
  } else if (ev.avoided) {
    tone = "var(--avoid)"; bg = "var(--avoid-soft)";
    text = `Eligible, but avoided — it has ${(ev.avoided_formats ?? []).join(", ")}. Grabbed only when nothing else is available.`;
  }
  return (
    <div className="rounded-lg px-3 py-2 text-[12px] font-semibold" style={{ background: bg, color: tone, border: `1px solid ${tone}` }}>
      {text}
      {ev.eligible && (ev.matched ?? []).length > 0 && (
        <div className="mt-0.5 text-[11px] font-normal text-ink-dim">Matches what you prefer: {(ev.matched ?? []).join(", ")}</div>
      )}
    </div>
  );
}

// stateOf names how the profile treats a format, in the builder's own words.
function stateOf(f: FormatCheck): { label: string; color: string } {
  if (f.required) return { label: "Must", color: "var(--accent)" };
  if (f.score > 0) return { label: "Prefer", color: "var(--good)" };
  if (f.score < 0) return { label: "Avoid", color: "var(--reject)" };
  return { label: "—", color: "var(--ink-faint)" };
}

function FormatTable({ formats }: { formats: FormatCheck[] }) {
  if (formats.length === 0) return null;
  return (
    <div className="overflow-hidden rounded-lg" style={{ border: "1px solid var(--line)" }}>
      <div className="flex px-3 py-1.5 font-mono text-[9px] font-bold uppercase tracking-[0.09em] text-ink-faint" style={{ background: "var(--panel-2)" }}>
        <span className="flex-1">Format</span><span className="w-[56px] text-center">Profile</span><span className="w-[48px] text-right">Has it</span>
      </div>
      {formats.map((f) => {
        const st = stateOf(f);
        return (
          <div key={f.name} className="flex items-center px-3 py-1 text-[11.5px]" style={{ borderTop: "1px solid var(--line-soft)" }}>
            <span className="min-w-0 flex-1 truncate">{f.name}{f.custom && <span className="ml-1.5 text-[10px] text-ink-faint">custom</span>}</span>
            <span className="w-[56px] text-center text-[10.5px] font-semibold" style={{ color: st.color }}>{st.label}</span>
            <span className="w-[48px] text-right font-mono" style={{ color: f.matched ? "var(--good)" : "var(--ink-faint)" }} aria-label={f.matched ? "matched" : "not matched"}>{f.matched ? "✓" : "·"}</span>
          </div>
        );
      })}
    </div>
  );
}
