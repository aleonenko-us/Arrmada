import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError, type Job, type MovieScanResult } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { useLiveEvent } from "../../lib/live";
import { jobDone, jobFailed, jobToast, useJob } from "../../lib/useJob";
import { usePoll } from "../../lib/usePoll";
import { Button, Modal } from "../../ui";

// The Movies page's library scan: the dialog that starts one, a live progress bar while it
// runs, and a summary when it ends that links to the folders needing review. The scan is a
// job; its progress comes over the page's one live socket (job.updated), so there's no
// timed polling of the grid any more.

export interface ScanOpts { monitor: boolean; quality_profile?: string }

// A scan of a big library runs for a long time; follow it for as long as the server would
// let it run (its budget is two seconds a folder).
const FOLLOW_SCAN_MS = 12 * 60 * 60 * 1000;
// While a scan runs the grid is refetched now and then, so films appear as they land.
const GRID_REFRESH_MS = 15_000;

export interface MovieScan {
  start: (opts: ScanOpts) => Promise<void>;
  running: boolean;
  job: Job | null;
  summary: MovieScanResult | null;
  dismiss: () => void;
}

// useMovieScan starts and follows the movie library scan. It also picks up a scan already
// running (from Settings → Library, or another tab), so the progress shows wherever the
// owner looks. onChanged refetches the grid; flash says something in the page's toast.
export function useMovieScan(onChanged: () => void, flash: (msg: string, err?: boolean) => void): MovieScan {
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [summary, setSummary] = useState<MovieScanResult | null>(null);
  const job = useJob(jobId, {
    giveUpMs: FOLLOW_SCAN_MS,
    onDone: (j) => {
      setJobId(null);
      onChanged();
      if (jobFailed(j) || j.status === "cancelled") flash(jobToast(j, "Scan finished."), true);
      else setSummary((j.result as MovieScanResult | undefined) ?? { imported: 0, skipped: 0, unmatched: 0, attached: 0, duplicates: 0 });
    },
  });

  useEffect(() => {
    api.jobs({ kind: "movie.scan", limit: 5 })
      .then((js) => {
        const live = js.find((j) => j.kind === "movie.scan" && !jobDone(j.status));
        if (live) setJobId((cur) => cur ?? live.id);
      })
      .catch(() => {});
  }, []);
  useLiveEvent("job.updated", (ev) => {
    const d = ev.data as { id?: number; kind?: string; status?: Job["status"] } | null;
    if (d?.id && d.kind === "movie.scan" && !jobDone(d.status)) setJobId((cur) => cur ?? d.id ?? null);
  });

  const running = starting || job.running;
  usePoll(onChanged, job.running ? GRID_REFRESH_MS : null, { immediate: false });

  const start = async (opts: ScanOpts) => {
    setStarting(true);
    setSummary(null);
    try {
      const r = await api.scanLibrary(opts);
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      // One scan at a time: the answer names the one running, so show its progress.
      const id = e instanceof ApiError && e.status === 409 ? Number(e.body?.job_id) : 0;
      if (id) {
        setJobId(id);
        flash("A library scan is already running — showing its progress.");
      } else {
        flash((e as Error).message, true);
      }
    } finally {
      setStarting(false);
    }
  };

  return { start, running, job: job.job, summary, dismiss: () => setSummary(null) };
}

const n = (x: number) => x.toLocaleString();

// ScanStatus is the progress bar while a scan runs, then its summary until dismissed.
export function ScanStatus({ scan }: { scan: MovieScan }) {
  if (scan.running) {
    const pct = Math.round((scan.job?.progress ?? 0) * 100);
    const msg = scan.job?.status === "queued" ? "Waiting for another library scan to finish…" : scan.job?.message || "Starting the scan…";
    return (
      <div role="status" className="mb-4 rounded-xl px-3.5 py-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <div className="flex items-center justify-between gap-3 text-[12px]">
          <span className="font-semibold">{msg}</span>
          <span className="font-mono text-[11px] text-ink-faint">{pct}%</span>
        </div>
        <div className="mt-2 h-1.5 overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }}>
          <div className="h-full rounded-full transition-[width]" style={{ width: `${pct}%`, background: "var(--accent)" }} />
        </div>
      </div>
    );
  }
  const s = scan.summary;
  if (!s) return null;
  const parts = [`${n(s.imported)} added`];
  if (s.attached) parts.push(`${n(s.attached)} attached`);
  if (s.duplicates) parts.push(`${n(s.duplicates)} left alone (already have a file)`);
  return (
    <div role="status" className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-xl px-3.5 py-2.5 text-[12.5px]" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
      <span className="font-semibold">Scan finished</span>
      <span className="text-ink-dim">{parts.join(" · ")}</span>
      {s.unmatched > 0 && (
        <Link to={LINKS.libraryReview} className="font-semibold" style={{ color: "var(--accent)" }}>
          · {n(s.unmatched)} need review →
        </Link>
      )}
      <button onClick={scan.dismiss} aria-label="Dismiss" className="ml-auto rounded-md px-2 py-0.5 text-[11px] text-ink-faint hover:text-[var(--ink)]">✕</button>
    </div>
  );
}

// ScanDialog asks how a library scan should catalog the films it finds. By default they're
// added unmonitored, so Arrmada never searches for or upgrades them; monitoring them on a
// profile lets the upgrade sweep replace their files. Films already in the library get the
// file found either way.
export function ScanDialog({ profiles, onScan, onClose }: {
  profiles: { key: string; name: string }[];
  onScan: (opts: ScanOpts) => void;
  onClose: () => void;
}) {
  const [monitor, setMonitor] = useState(false);
  const [profile, setProfile] = useState(""); // "" = the default profile
  return (
    <Modal
      onClose={onClose}
      size="sm"
      title="Catalog existing films"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="primary" onClick={() => onScan(monitor ? { monitor: true, quality_profile: profile } : { monitor: false })}>Scan library</Button>
        </>
      }
    >
      <p className="m-0 text-[12.5px] text-ink-dim">
        Arrmada adds the films it finds in your movies folder, and gives films already in your library the file it finds for them.
      </p>
      <label className="mt-4 flex items-start gap-2.5 text-[12.5px]">
        <input type="checkbox" className="mt-0.5" checked={monitor} onChange={(e) => setMonitor(e.target.checked)} />
        <span>
          Monitor them so they can be upgraded
          <span className="mt-0.5 block text-[11.5px] text-ink-faint">Unmonitored films are never searched for or upgraded — Arrmada only catalogs them.</span>
        </span>
      </label>
      {monitor && (
        <label className="mt-3 flex flex-wrap items-center gap-2 text-[12px]">
          <span className="font-mono text-[10.5px] uppercase text-ink-faint">Profile</span>
          <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
            <option value="">Default profile</option>
            {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
          </select>
        </label>
      )}
    </Modal>
  );
}
