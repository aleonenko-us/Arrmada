import { useState } from "react";
import { req, type SubFileEntry, type SubLangHealth, type SubLangStatus } from "../../lib/api";
import { jobToast, useJob } from "../../lib/useJob";

// Health: how well each present subtitle lines up with its video's speech. Downloads
// without a hash match are checked (and shifted into sync) when they're saved; any other
// sidecar can be checked here. The call lives in the Subtitles chunk, not lib/api.ts.

const syncApi = {
  check: (f: SubFileEntry, lang: string) =>
    req<{ job_id?: number; file: string }>("/api/v1/subtitles/sync-check", {
      method: "POST",
      body: JSON.stringify({ kind: f.kind, movie_id: f.movie_id, series_id: f.series_id, season: f.season, episode: f.episode, lang }),
    }),
};

const TONE: Record<SubLangHealth["state"], { color: string; background: string }> = {
  ok: { color: "var(--good-text)", background: "var(--good-soft)" },
  uncertain: { color: "var(--avoid-text)", background: "var(--avoid-soft)" },
  bad: { color: "var(--reject-text)", background: "var(--reject-soft)" },
  unknown: { color: "var(--ink-dim)", background: "var(--panel-2)" },
  unchecked: { color: "var(--ink-faint)", background: "transparent" },
};

// HealthCell is one file's Health column: a chip per present language. A chip checks
// that language's sync when clicked; the check reads the soundtrack, so it runs as a
// job and the row reloads when it's done.
export function HealthCell({ f, flash, onChanged }: { f: SubFileEntry; flash: (m: string) => void; onChanged: () => void }) {
  const present = (f.languages ?? []).filter((l) => l.have);
  if (present.length === 0) return <span className="text-ink-faint">—</span>;
  return (
    <div className="flex flex-wrap items-center gap-1">
      {present.map((l) => <HealthChip key={l.lang} f={f} l={l} flash={flash} onChanged={onChanged} />)}
    </div>
  );
}

function HealthChip({ f, l, flash, onChanged }: { f: SubFileEntry; l: SubLangStatus; flash: (m: string) => void; onChanged: () => void }) {
  const [jobId, setJobId] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  useJob(jobId, {
    giveUpMs: 30 * 60 * 1000, // a remux's soundtrack takes a minute or more to read
    onDone: (job) => { flash(jobToast(job, "Sync checked.")); setJobId(null); setBusy(false); onChanged(); },
  });
  const h = l.health ?? { state: "unchecked" as const, note: "not checked" };
  const checked = h.state !== "unchecked";
  const label = busy ? "checking…" : h.state === "unchecked" ? "not checked" : h.state === "unknown" ? "unknown" : String(h.score ?? "");
  const run = async () => {
    setBusy(true);
    try {
      const r = await syncApi.check(f, l.lang);
      flash(`Checking the sync of ${r.file} — reading the soundtrack takes a minute`);
      if (r.job_id) setJobId(r.job_id);
      else setBusy(false);
    } catch (e) { flash((e as Error).message); setBusy(false); }
  };
  return (
    <button
      type="button"
      onClick={run}
      disabled={busy}
      title={`${l.lang.toUpperCase()}: ${h.note}\nClick to check its sync against the audio now.`}
      className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase disabled:opacity-60"
      style={{ ...TONE[h.state], border: checked ? "1px solid transparent" : "1px dashed var(--line)" }}
    >
      {l.lang} · {label}
    </button>
  );
}
