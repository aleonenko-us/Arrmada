import { Suspense, useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { formatAgo } from "../lib/format";
import { posterThumb } from "../lib/img";
import { lazyPage } from "../lib/lazyPage";
import { ISSUE_TONE, ISSUE_TOPICS, issueCategoryLabel, issueEpisode, issueStatusLabel, issuesApi, type Issue, type IssueCounts } from "../lib/issuesApi";
import { useLiveRefresh } from "../lib/useLiveRefresh";
import { EmptyState, ErrorState, StatusChip } from "../ui";

// The staff sheet for one issue (fixes, resolve, dismiss), loaded when one is opened.
const IssueSheet = lazyPage(() => import("./IssueSheet"), "IssueSheet");

// IssuesPanel is the Requests page's Issues tab: problems people reported with titles in
// the library. Staff see every report and open one to fix, resolve or dismiss it; a
// requester sees the ones they reported, where each has got to, and what they were told.
//
//   ?show=closed   the closed ones (resolved or dismissed) instead of the open ones
//   ?issue=<id>    the issue's sheet open (staff), from a link or a notification
export function IssuesPanel({ staff, onCounts }: { staff: boolean; onCounts: (c: IssueCounts) => void }) {
  const [params, setParams] = useSearchParams();
  const show = params.get("show") === "closed" ? "closed" : "open";
  const openID = Number(params.get("issue")) || 0;
  const [items, setItems] = useState<Issue[] | null>(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const setParam = (key: string, value: string) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (value) next.set(key, value); else next.delete(key);
      return next;
    });

  const load = useCallback(async () => {
    try {
      const r = await issuesApi.list({ status: show });
      setItems(r.issues);
      setTotal(r.total);
      onCounts(r.counts);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }, [show, onCounts]);
  useEffect(() => { void load(); }, [load]);
  // A report, a fix starting or an issue closing anywhere reloads the list; a slow poll
  // backs that up.
  useLiveRefresh(load, { topics: ISSUE_TOPICS, fallbackMs: 60_000 });

  const open = openID ? (items ?? []).find((i) => i.id === openID) : undefined;
  return (
    <>
      <div className="mb-4 inline-flex rounded-lg p-0.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }} role="group" aria-label="Which problems">
        {(["open", "closed"] as const).map((k) => (
          <button
            key={k}
            onClick={() => setParam("show", k === "closed" ? "closed" : "")}
            aria-pressed={show === k}
            className="min-h-[32px] rounded-md px-3 text-sm font-semibold"
            style={{ background: show === k ? "var(--accent)" : "transparent", color: show === k ? "var(--accent-ink)" : "var(--ink-faint)" }}
          >
            {k === "open" ? "Open" : "Closed"}
          </button>
        ))}
      </div>
      {error && !items ? (
        <ErrorState what="reported problems" message={error} onRetry={() => { void load(); }} />
      ) : !items ? (
        <div className="py-10 text-center text-base text-ink-dim">Loading…</div>
      ) : items.length === 0 ? (
        <EmptyState
          title={show === "open" ? "No open problems" : "Nothing closed yet"}
          body={show === "open" ? (staff ? "When someone reports a problem with a title, it shows up here." : "Something wrong with a movie or episode? Open it on Discover and tap Report a problem.") : ""}
        />
      ) : (
        <>
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {items.map((i) => <IssueRow key={i.id} issue={i} staff={staff} onOpen={staff ? () => setParam("issue", String(i.id)) : undefined} />)}
          </ul>
          {items.length < total && <p className="mt-3 text-center text-sm text-ink-faint">Showing the newest {items.length} of {total}.</p>}
        </>
      )}
      {staff && openID > 0 && (
        <Suspense fallback={null}>
          <IssueSheet key={openID} issueId={openID} initial={open} onChanged={() => { void load(); }} onClose={() => setParam("issue", "")} />
        </Suspense>
      )}
    </>
  );
}


function IssueRow({ issue: i, staff, onOpen }: { issue: Issue; staff: boolean; onOpen?: () => void }) {
  const ep = issueEpisode(i);
  const note = staff ? i.note : i.my_note;
  const who = staff && i.reported_by_name ? `${i.reported_by_name}${i.reporter_count > 1 ? ` +${i.reporter_count - 1}` : ""} · ` : "";
  const body = (
    <>
      <div className="h-[72px] w-[48px] flex-none overflow-hidden rounded-lg" style={{ background: "var(--panel-2)" }}>
        {i.poster_url && <img src={posterThumb(i.poster_url)} alt="" className="h-full w-full object-cover" loading="lazy" decoding="async" />}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 truncate text-md font-semibold" style={{ color: "var(--ink)" }}>{i.title}</span>
          {ep ? <span className="font-mono text-xs text-ink-dim">{ep}</span> : <span className="font-mono text-xs text-ink-faint">{i.year || ""}</span>}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <StatusChip tone={ISSUE_TONE[i.status]}>{issueStatusLabel(i.status)}</StatusChip>
          <span className="text-ink-dim">{issueCategoryLabel(i.category)}</span>
        </div>
        <div className="mt-1 truncate font-mono text-xs text-ink-faint">{who}{formatAgo(i.created_at * 1000, Date.now())}</div>
        {note && <div className="mt-1 line-clamp-2 text-sm italic text-ink-dim">“{note}”</div>}
        {i.resolution && (i.status === "resolved" || i.status === "dismissed") && <div className="mt-1 text-sm text-ink-dim">{i.resolution}</div>}
        {staff && i.action_note && <div className="mt-1 text-sm" style={{ color: "var(--avoid-text)" }}>{i.action_note}</div>}
      </div>
    </>
  );
  return (
    <li className="rounded-xl p-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      {onOpen ? (
        <button onClick={onOpen} className="flex w-full min-w-0 items-start gap-3 text-left" aria-label={`Open the problem with ${i.title}${ep ? ` ${ep}` : ""}`}>{body}</button>
      ) : (
        <div className="flex min-w-0 items-start gap-3">{body}</div>
      )}
    </li>
  );
}
