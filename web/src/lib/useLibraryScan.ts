import { useEffect, useState } from "react";
import { api, ApiError, type Job } from "./api";
import { useLiveEvent } from "./live";
import { jobDone, jobFailed, useJob } from "./useJob";

// Following one library's scan from Settings → Library: start it, show its progress as it
// runs ("Scanning 120 / 412 folders · 29%") and what it found when it ends. A scan is a
// job, so its progress arrives over the page's one live socket (job.updated) rather than
// by reloading after a fixed wait. A scan already running — started here, on the Movies
// page or in another tab — is picked up too, and a second Scan follows the running one.

// A scan of a big library runs for a long time; follow it for as long as the server lets it.
const FOLLOW_SCAN_MS = 12 * 60 * 60 * 1000;

export interface LibraryScan {
  start: () => Promise<void>;
  /** Stop the scan being followed. It ends as stopped and keeps what it finished. */
  stop: () => Promise<void>;
  /** Starting, waiting for its turn or scanning. */
  running: boolean;
  /** The job being followed (live progress and message) or, once it ends, the finished one. */
  job: Job | null;
  /** A note about the last Scan press ("already scanning") or why it couldn't start. */
  note: { text: string; error: boolean } | null;
}

// target, when set, follows only scans of that target: "all" for a whole library, or
// "folder:<id>" for one library folder (Settings → Library's extra folders), so a row
// doesn't show another folder's scan as its own.
export function useLibraryScan(kind: string, begin: () => Promise<{ job_id?: number }>, onFinished?: (job: Job) => void, target?: string): LibraryScan {
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [finished, setFinished] = useState<Job | null>(null);
  const [note, setNote] = useState<LibraryScan["note"]>(null);
  const followed = useJob(jobId, {
    giveUpMs: FOLLOW_SCAN_MS,
    onDone: (j) => {
      setJobId(null);
      setFinished(j);
      onFinished?.(j);
    },
  });

  // A scan that's already running when the page opens.
  useEffect(() => {
    if (!kind) return; // a row with no scan (Downloads)
    let live = true;
    api.jobs({ kind, target, limit: 5 })
      .then((js) => {
        const running = js.find((j) => j.kind === kind && (!target || j.target === target) && !jobDone(j.status));
        if (live && running) setJobId((cur) => cur ?? running.id);
      })
      .catch(() => {});
    return () => { live = false; };
  }, [kind, target]);
  // One started elsewhere while the page is open.
  useLiveEvent("job.updated", (ev) => {
    const d = ev.data as { id?: number; kind?: string; target?: string; status?: Job["status"] } | null;
    if (d?.id && d.kind === kind && (!target || d.target === target) && !jobDone(d.status)) setJobId((cur) => cur ?? d.id ?? null);
  }, !!kind);

  const start = async () => {
    setStarting(true);
    setNote(null);
    setFinished(null);
    try {
      const r = await begin();
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      // One scan at a time: the refusal names the running one, so follow it.
      const id = e instanceof ApiError && e.status === 409 ? Number(e.body?.job_id) : 0;
      if (id) {
        setJobId(id);
        setNote({ text: "Already scanning — showing its progress.", error: false });
      } else {
        setNote({ text: (e as Error).message, error: true });
      }
    } finally {
      setStarting(false);
    }
  };

  // A scan may run for hours on a big library, so it can be stopped from its row. The call
  // is staff-only, so it loads with the stop, not with anyone's first page.
  const stop = async () => {
    const id = followed.job?.id ?? jobId;
    if (!id) return;
    try {
      const { systemApi } = await import("./systemApi");
      await systemApi.cancelJob(id);
    } catch (e) {
      // Already over: its end arrives as job.updated like any other.
      if (!(e instanceof ApiError && e.status === 409)) setNote({ text: (e as Error).message, error: true });
    }
  };

  return { start, stop, running: starting || followed.running, job: followed.job ?? finished, note };
}

// scanLine is the one line a library row shows about its scan, or null when there's
// nothing to say. Running: the job's own progress words; ended: what it found.
export function scanLine(label: string, scan: LibraryScan): { text: string; tone: "dim" | "good" | "error" } | null {
  const j = scan.job;
  if (scan.running) {
    if (j?.status === "queued") return { text: `${label}: waiting for another library scan to finish…`, tone: "dim" };
    if (!j?.message) return { text: `Scanning ${label}…`, tone: "dim" };
    return { text: `${label}: ${j.message} · ${Math.round((j.progress ?? 0) * 100)}%`, tone: "dim" };
  }
  if (scan.note?.error) return { text: scan.note.text, tone: "error" };
  if (j && jobDone(j.status)) {
    if (jobFailed(j)) return { text: `Scan failed: ${j.error || "it stopped with an error"}`, tone: "error" };
    if (j.status === "cancelled") return { text: "Scan stopped.", tone: "dim" };
    return { text: `Done: ${j.message || "scan finished"}`, tone: "good" };
  }
  return null;
}

// foundLine sums up a finished scan of one folder: "Found 3 titles; 1 needs review". The
// titles found are the ones it added plus, for films, the files it gave to films already in
// the library.
export function foundLine(job: Job): string {
  const r = (job.result ?? {}) as { imported?: unknown; attached?: unknown };
  const n = (v: unknown) => (typeof v === "number" ? v : 0);
  const found = n(r.imported) + n(r.attached);
  const review = unmatchedOf(job);
  const titles = `Found ${found.toLocaleString()} title${found === 1 ? "" : "s"}`;
  return review > 0 ? `${titles}; ${review.toLocaleString()} need${review === 1 ? "s" : ""} review` : `${titles}.`;
}

// musicReviewsOf is how many folders a finished music scan left in Reviews to be matched
// (MUS-37: its result's reviews).
export function musicReviewsOf(job: Job | null): number {
  if (!job || job.status !== "succeeded") return 0;
  const r = job.result as { reviews?: unknown } | null | undefined;
  return typeof r?.reviews === "number" ? r.reviews : 0;
}

// unmatchedOf is how many folders a finished scan couldn't identify (its result's count).
export function unmatchedOf(job: Job | null): number {
  if (!job || job.status !== "succeeded") return 0;
  const r = job.result as { unmatched?: unknown } | null | undefined;
  return typeof r?.unmatched === "number" ? r.unmatched : 0;
}
