import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError, type Job, type MovieScanResult } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { useLiveEvent } from "../../lib/live";
import { jobDone, jobFailed, jobToast, useJob } from "../../lib/useJob";
import { usePoll } from "../../lib/usePoll";

// The Movies page's library scan (MOV-20): live progress while it runs (the Library tools
// chip, scanProgressLabel) and a summary when it ends that links to the folders needing
// review. The scan is a job; its progress comes over the page's one live socket
// (job.updated), so there's no timed polling of the grid for it any more. The dialog that
// starts one is pages/movies/ScanDialog.

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

// scanProgressLabel is the running scan's progress for the toolbar chip ("Scanning 312 /
// 1,204 folders · 26%"), or undefined when no scan runs.
export function scanProgressLabel(scan: MovieScan): string | undefined {
  if (!scan.running) return undefined;
  const j = scan.job;
  if (j?.status === "queued") return "Scan waiting for another library scan…";
  if (!j?.message) return "Scanning…";
  return `${j.message} · ${Math.round((j.progress ?? 0) * 100)}%`;
}

// ScanStatus is a finished scan's summary, until dismissed (a running scan shows as the
// toolbar chip).
export function ScanStatus({ scan }: { scan: MovieScan }) {
  const s = scan.running ? null : scan.summary;
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

