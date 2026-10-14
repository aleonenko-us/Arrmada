import { useState } from "react";
import { Link } from "react-router-dom";
import type { JobStatus, RecentJob } from "../../lib/api";
import { systemApi } from "../../lib/systemApi";
import { ago, took } from "../../lib/taskTime";
import { useLiveRefresh } from "../../lib/useLiveRefresh";
import { StatusChip, type Tone } from "../../ui";

// RecentJobs: the work people and events started — a Search click, an approval's search,
// a scan, a Run now — newest first, under the scheduled tasks. Each row says what it was
// in words, links to its title, says who started it, and how it went (a failure's error
// opens under it). It follows job.updated on the live socket, with a 15 s backstop poll
// that stops while the tab is hidden.

// Columns on a wide screen; below 640px each job is a card.
const grid = "sm:grid sm:grid-cols-[minmax(0,1fr)_minmax(0,140px)_84px_84px] sm:items-center sm:gap-x-3";

const STATUS: Record<JobStatus, { label: string; tone: Tone }> = {
  queued: { label: "Waiting", tone: "faint" },
  running: { label: "Running", tone: "accent" },
  succeeded: { label: "OK", tone: "good" },
  failed: { label: "Failed", tone: "reject" },
  panicked: { label: "Failed", tone: "reject" },
  cancelled: { label: "Cancelled", tone: "faint" },
  interrupted: { label: "Interrupted", tone: "avoid" },
};

export function RecentJobs() {
  const [list, setList] = useState<RecentJob[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  useLiveRefresh(
    () => systemApi.recentJobs().then((j) => { setList(j); setErr(null); }).catch((e: Error) => setErr(e.message)),
    { topics: ["job.updated"], fallbackMs: 15_000, immediate: true },
  );

  return (
    <div className="flex flex-col gap-2">
      <h3 className="m-0 font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Recent jobs</h3>
      {err && <p className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>Couldn't load the recent jobs: {err}</p>}
      <div className="rounded-lg" style={{ border: "1px solid var(--line)" }}>
        {list === null ? (
          <div className="p-4 text-center text-sm text-ink-dim">{err ? "Couldn't load the recent jobs." : "Loading…"}</div>
        ) : list.length === 0 ? (
          <div className="p-4 text-center text-sm text-ink-dim">Nothing has run yet. A search, a scan or a Run now shows here.</div>
        ) : (
          list.map((j, i) => {
            const st = STATUS[j.status] ?? { label: j.status, tone: "faint" as Tone };
            const failed = st.tone === "reject" && !!j.error;
            const showErr = open === j.id && failed;
            return (
              <div key={j.id} className="px-3 py-2" style={{ borderTop: i === 0 ? undefined : "1px solid var(--line-soft)" }}>
                <div className={`flex flex-col gap-1 ${grid}`}>
                  <div className="min-w-0">
                    <div className="truncate text-base font-semibold" title={j.kind}>
                      {j.label}
                      {j.target_label && (
                        <>
                          <span className="text-ink-faint"> · </span>
                          {j.target_path ? <Link to={j.target_path} style={{ color: "var(--accent-text)" }}>{j.target_label}</Link> : <span>{j.target_label}</span>}
                        </>
                      )}
                    </div>
                    {j.message && !failed && <div className="truncate text-xs text-ink-faint" title={j.message}>{j.message}</div>}
                  </div>
                  <div className="min-w-0 truncate text-sm text-ink-dim">
                    {j.trigger_path ? <Link to={j.trigger_path} style={{ color: "var(--accent-text)" }}>{j.trigger_label}</Link> : j.trigger_label}
                  </div>
                  <div className="text-sm text-ink-dim">
                    {ago(j.created_at)}
                    {j.duration_ms > 0 && <span className="text-ink-faint"> · {took(j.duration_ms)}</span>}
                  </div>
                  <div className="sm:text-right">
                    {failed ? (
                      <button type="button" onClick={() => setOpen(showErr ? null : j.id)} aria-expanded={showErr} title={j.error}>
                        <StatusChip tone={st.tone} size="xs">{st.label}</StatusChip>
                      </button>
                    ) : (
                      <StatusChip tone={st.tone} size="xs">{st.label}</StatusChip>
                    )}
                  </div>
                </div>
                {showErr && (
                  <div className="mt-2 break-words rounded-md p-2 font-mono text-xs" style={{ background: "var(--reject-soft)", color: "var(--reject-text)" }}>{j.error}</div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
