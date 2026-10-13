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
  /** Starting, waiting for its turn or scanning. */
  running: boolean;
  /** The job being followed (live progress and message) or, once it ends, the finished one. */
  job: Job | null;
  /** A note about the last Scan press ("already scanning") or why it couldn't start. */
  note: { text: string; error: boolean } | null;
}

export function useLibraryScan(kind: string, begin: () => Promise<{ job_id?: number }>, onFinished?: (job: Job) => void): LibraryScan {
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
    api.jobs({ kind, limit: 5 })
      .then((js) => {
        const running = js.find((j) => j.kind === kind && !jobDone(j.status));
        if (live && running) setJobId((cur) => cur ?? running.id);
      })
      .catch(() => {});
    return () => { live = false; };
  }, [kind]);
  // One started elsewhere while the page is open.
  useLiveEvent("job.updated", (ev) => {
    const d = ev.data as { id?: number; kind?: string; status?: Job["status"] } | null;
    if (d?.id && d.kind === kind && !jobDone(d.status)) setJobId((cur) => cur ?? d.id ?? null);
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

  return { start, running: starting || followed.running, job: followed.job ?? finished, note };
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

// unmatchedOf is how many folders a finished scan couldn't identify (its result's count).
export function unmatchedOf(job: Job | null): number {
  if (!job || job.status !== "succeeded") return 0;
  const r = job.result as { unmatched?: unknown } | null | undefined;
  return typeof r?.unmatched === "number" ? r.unmatched : 0;
}
