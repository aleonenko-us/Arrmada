import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, type MediaRequest, type PreviewRelease, type RequestPreview } from "../lib/api";
import { requestPreviewApi } from "../lib/requestPreviewApi";
import { useJob, jobFailed, jobToast } from "../lib/useJob";
import { useLiveEvent } from "../lib/live";
import { libraryPath } from "../lib/requestStage";
import { checkedAgo, grabApplies, previewHeadline, previewLine, reasonsLine, releaseFacts, seasonLine } from "../lib/requestPreview";
import { Button, StatusChip } from "../ui";
import { ReleaseExplain } from "./ReleaseExplain";

// The release preview of a request waiting for approval: what approving it would grab,
// looked up in advance, or why it wouldn't grab anything yet. Staff only — this module is
// loaded lazily from the request sheet and list for staff, so a requester never downloads
// it (and the server never sends them a preview).

/** What "Approve & grab this" would take, for the sheet's button. */
export interface PreviewGrab {
  checkedAt: number;
  title: string;
}

const SHOWN_RELEASES = 4;

export function RequestPreviewPanel({ rq, profile, profileName, keep, onGrab }: {
  rq: MediaRequest;
  /** The quality profile picked in the sheet ("" while the list loads). */
  profile: string;
  profileName: (key: string) => string;
  /** A series being trimmed: the seasons left ticked (null: as asked). */
  keep: number[] | null;
  /** Told whenever "Approve & grab this" starts or stops applying. */
  onGrab: (g: PreviewGrab | null) => void;
}) {
  const [preview, setPreview] = useState<RequestPreview | undefined>(rq.preview);
  const [jobId, setJobId] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const asked = useRef(false);
  // The sheet's fresh copy of the request carries the newest preview.
  const [given, setGiven] = useState(rq.preview);
  if (rq.preview !== given) {
    setGiven(rq.preview);
    if (rq.preview) setPreview(rq.preview);
  }

  const reload = useCallback(async () => {
    const r = await api.getRequest(rq.id).catch(() => null);
    if (r) setPreview(r.request.preview);
  }, [rq.id]);
  const job = useJob(jobId, {
    onDone: (j) => {
      setJobId(null);
      if (jobFailed(j)) setError(jobToast(j));
      void reload();
    },
  });
  // A check the queue finished (or another staff member's) lands while the sheet is open.
  useLiveEvent("request.updated", (ev) => {
    const d = ev.data as { id?: number; preview?: boolean } | null;
    if (d?.id === rq.id && d.preview && !jobId) void reload();
  });

  const check = useCallback(async (p: string) => {
    setError(null);
    try {
      const ref = await requestPreviewApi.refresh(rq.id, p || undefined);
      if (ref.job_id) setJobId(ref.job_id);
      else await reload();
    } catch (e) {
      setError((e as Error).message);
    }
  }, [rq.id, reload]);

  // Opening the request checks again when the preview is missing or older than six hours.
  useEffect(() => {
    if (asked.current || !preview?.stale || preview.state === "checking") return;
    asked.current = true;
    void check(profile);
  }, [preview, profile, check]);

  const checking = !!jobId || preview?.state === "checking";
  const applies = !checking && grabApplies(preview, profile, keep);
  const grabRef = useRef(onGrab);
  useEffect(() => { grabRef.current = onGrab; });
  const firstTitle = preview?.result?.releases[0]?.title ?? "";
  useEffect(() => {
    grabRef.current(applies && preview ? { checkedAt: preview.checked_at, title: firstTitle } : null);
  }, [applies, preview, firstTitle]);

  const r = preview?.result;
  const head = previewHeadline(preview);
  const otherProfile = !!(preview?.profile && profile && preview.profile !== profile && r);
  const lib = libraryPath(rq);
  const season = r ? seasonLine(r) : "";
  return (
    <section aria-label="Release preview" data-testid="release-preview" className="flex flex-col gap-2 rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-2xs uppercase text-ink-faint">Release preview</span>
        {checking ? (
          <span className="text-sm text-ink-dim" role="status">{job.message || "Checking indexers…"}</span>
        ) : (
          <StatusChip tone={head.tone}>{head.title}</StatusChip>
        )}
        {!checking && preview?.checked_at ? <span className="text-xs text-ink-faint">checked {checkedAgo(preview.checked_at)}</span> : null}
        {!checking && otherProfile && <span className="text-xs font-semibold" style={{ color: "var(--avoid-text)" }}>for {profileName(preview!.profile)}</span>}
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void check(profile)} disabled={checking}>
          {otherProfile ? `Check for ${profileName(profile)}` : "Check again"}
        </Button>
      </div>

      {r && r.outcome === "found" && (
        <div className="flex flex-col gap-1.5">
          {season && <div className="text-sm font-semibold">{season}</div>}
          {r.releases.slice(0, SHOWN_RELEASES).map((rel) => <ReleaseLine key={rel.title} rel={rel} />)}
          {r.releases.length > SHOWN_RELEASES && (
            <div className="text-xs text-ink-faint">+ {r.releases.length - SHOWN_RELEASES} more</div>
          )}
          {r.releases.length === 1 && (
            <ReleaseExplain matched={r.releases[0].matched} avoided={r.releases[0].avoided} />
          )}
          {r.why && r.why.length > 0 && <div className="text-sm text-ink-dim">Why: {r.why.join(" · ")}</div>}
          {r.chosen_over && <div className="text-xs text-ink-faint">{r.chosen_over}</div>}
        </div>
      )}
      {r && r.outcome === "none" && reasonsLine(r) && <div className="text-sm text-ink-dim">{reasonsLine(r)}</div>}
      {r && r.outcome === "unavailable" && (r.indexer_issues ?? []).length > 0 && (
        <ul className="m-0 list-none p-0 text-sm text-ink-dim">
          {r.indexer_issues!.map((is) => (
            <li key={is.indexer}><b className="text-ink">{is.indexer}</b> · {is.skipped ? "paused after repeated failures" : is.error}</li>
          ))}
        </ul>
      )}
      {r?.seasons_note && <div className="text-sm text-ink-dim">{r.seasons_note}</div>}
      {r?.outcome === "busy" && <div className="text-sm text-ink-dim">It’s checked again in a few minutes.</div>}
      {lib && <Link to={lib} className="self-start text-sm font-semibold" style={{ color: "var(--accent-text)" }}>See all releases</Link>}
      {error && <div className="text-sm font-medium" style={{ color: "var(--reject-text)" }} role="alert">{error}</div>}
    </section>
  );
}

function ReleaseLine({ rel }: { rel: PreviewRelease }) {
  return (
    <div className="min-w-0">
      <div className="truncate font-mono text-sm text-ink" title={rel.title}>{rel.covers ? `${rel.covers} · ` : ""}{rel.title}</div>
      <div className="flex flex-wrap gap-x-2 text-xs text-ink-dim">
        {releaseFacts(rel).map((f, i) => <span key={i}>{f}</span>)}
        <span className="text-ink-faint">{rel.indexer}</span>
      </div>
    </div>
  );
}

/** One line for a request's row in the list: "Would grab 1080p · BluRay · 12.0 GB". */
export function RequestPreviewLine({ preview }: { preview?: RequestPreview }) {
  const head = previewHeadline(preview);
  if (!preview?.result) return null;
  const color = head.tone === "good" ? "var(--good-text)" : head.tone === "avoid" ? "var(--avoid-text)" : "var(--ink-faint)";
  return <div className="mt-1 truncate text-sm" style={{ color }} data-testid="preview-line">{previewLine(preview)}</div>;
}
