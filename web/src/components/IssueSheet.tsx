import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { formatAgo } from "../lib/format";
import { posterThumb } from "../lib/img";
import { ISSUE_TONE, issueCategoryLabel, issueEpisode, issueStatusLabel, issuesApi, type Issue } from "../lib/issuesApi";
import { refreshAttention } from "../lib/useAttention";
import { Button, Sheet, StatusChip, useConfirm, useToast } from "../ui";

// The longest note staff can close an issue with (the server refuses longer).
const CLOSE_NOTE_MAX = 500;

// IssueSheet is one reported problem, for staff: what's wrong, who reported it and what
// they said, and what to do about it. Resolving or dismissing tells everyone who reported
// it, with the note when one is given.
//
// The one-click fixes put it in "fixing": "Blocklist this copy & find another" (confirmed
// first — the current file goes to the recycle bin when the new one is imported) or
// "Make the subtitles again". It then resolves itself when the new file lands or the
// subtitle job has made subtitles, and the reporters are told.
export function IssueSheet({ issueId, initial, onChanged, onClose }: {
  issueId: number;
  /** What the list already has, shown while the fresh copy loads. */
  initial?: Issue;
  onChanged: () => void;
  onClose: () => void;
}) {
  const flash = useToast();
  const confirm = useConfirm();
  const [issue, setIssue] = useState<Issue | null>(initial ?? null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  // Closing: which way, and the note the reporters are told.
  const [closing, setClosing] = useState<"resolve" | "dismiss" | null>(null);
  const [note, setNote] = useState("");

  const reload = () =>
    issuesApi.list({ id: issueId })
      .then((r) => {
        const i = r.issues[0];
        if (i) { setIssue(i); setLoadError(null); } else setLoadError("That problem isn’t there any more.");
      })
      .catch((e: Error) => setLoadError(e.message));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void reload(); }, [issueId]);

  if (!issue) {
    return (
      <Sheet onClose={onClose} closeOnBack={false} ariaLabel="Reported problem" size="sm">
        <div className="p-6 text-[12.5px] text-ink-dim">{loadError ?? "Loading…"}</div>
      </Sheet>
    );
  }

  const live = issue.status === "open" || issue.status === "fixing";
  const ep = issueEpisode(issue);
  const lib = issue.library_id ? `/${issue.media_type === "movie" ? "movies" : "series"}/${issue.library_id}` : null;
  const big = "min-h-[40px]";

  const close = async () => {
    if (!closing) return;
    setBusy(closing); setError(null);
    try {
      const done = closing === "resolve" ? await issuesApi.resolve(issue.id, note.trim()) : await issuesApi.dismiss(issue.id, note.trim());
      setIssue(done);
      setClosing(null); setNote("");
      flash(closing === "resolve" ? "Marked fixed — they’ve been told" : "Closed — they’ve been told");
      refreshAttention();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  const fix = async (action: "replace" | "subtitles") => {
    if (action === "replace") {
      const yes = await confirm({
        title: `Blocklist this copy of “${issue.title}”${ep ? ` ${ep}` : ""} and find another?`,
        body: "The release this file came from is blocklisted and a new copy is searched for. When it’s imported, the current file goes to the recycle bin, the problem is marked fixed and everyone who reported it is told.",
        confirmLabel: "Blocklist and search",
        tone: "danger",
      });
      if (!yes) return;
    }
    setBusy(action); setError(null);
    try {
      const started = await issuesApi.fix(issue.id, action);
      setIssue(started);
      flash(action === "replace" ? "Searching for another copy — it’ll resolve when it lands" : "Making the subtitles again — it’ll resolve when they’re done");
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };
  // A subtitles problem leads with the subtitle fix; anything else with a new copy.
  const subsFirst = issue.category === "subtitles";
  const fixes = [
    <Button key="replace" variant={subsFirst ? "secondary" : "primary"} className={big} onClick={() => { void fix("replace"); }} busy={busy === "replace"} busyLabel="Starting…" disabled={!!busy}>Blocklist this copy &amp; find another</Button>,
    <Button key="subtitles" variant={subsFirst ? "primary" : "secondary"} className={big} onClick={() => { void fix("subtitles"); }} busy={busy === "subtitles"} busyLabel="Starting…" disabled={!!busy}>Make the subtitles again</Button>,
  ];
  if (subsFirst) fixes.reverse();

  return (
    <Sheet onClose={onClose} closeOnBack={false} ariaLabel={`Problem with ${issue.title}`} size="lg" dismissible={!busy}>
      <div className="flex gap-4 p-5 sm:p-6">
        <div className="h-[120px] w-[80px] flex-none overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
          {issue.poster_url && <img src={posterThumb(issue.poster_url)} alt="" className="h-full w-full object-cover" />}
        </div>
        <div className="min-w-0 flex-1">
          <h2 className="m-0 text-[18px] font-bold leading-tight">{issue.title}</h2>
          <div className="mt-0.5 font-mono text-[11px] text-ink-faint">{ep || issue.year || ""}</div>
          <div className="mt-2.5 flex flex-wrap items-center gap-2">
            <StatusChip tone={ISSUE_TONE[issue.status]}>{issueStatusLabel(issue.status)}</StatusChip>
            <span className="text-[12.5px] font-semibold">{issueCategoryLabel(issue.category)}</span>
          </div>
          <div className="mt-2 text-[12px] text-ink-dim">
            Reported {formatAgo(issue.created_at * 1000, Date.now())}
            {issue.resolved_by_name && !live ? ` · closed by ${issue.resolved_by_name}` : ""}
          </div>
        </div>
      </div>

      <div className="flex flex-col gap-4 px-5 pb-5 sm:px-6 sm:pb-6">
        {/* Each reporter and what they said. Notes are plain text. */}
        <div className="flex flex-col gap-2">
          {(issue.reporters ?? []).map((r) => (
            <div key={r.user_id} className="rounded-lg p-3 text-[12.5px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
              <div className="font-mono text-[9.5px] uppercase text-ink-faint">{r.name || "Someone"} · {formatAgo(r.at * 1000, Date.now())}</div>
              {r.note ? <div className="mt-1 whitespace-pre-wrap text-ink-dim">{r.note}</div> : <div className="mt-1 text-ink-faint">No note</div>}
            </div>
          ))}
        </div>

        {issue.status === "fixing" && (
          <div className="rounded-lg p-3 text-[12.5px]" style={{ background: "var(--accent-soft)", border: "1px solid var(--line)" }}>
            <div className="font-semibold" style={{ color: "var(--accent)" }}>Fixing{issue.action_at ? ` · started ${formatAgo(issue.action_at * 1000, Date.now())}` : ""}</div>
            <div className="mt-0.5 text-ink-dim">{issue.action_note || "Waiting for the fix to land."} It resolves itself when it does, and they’re told.</div>
            {issue.action_file && <div className="mt-1 break-all font-mono text-[10.5px] text-ink-faint">{issue.action === "replace" ? "Replacing" : "File:"} {issue.action_file}</div>}
          </div>
        )}
        {issue.status === "open" && issue.action_note && <div className="text-[12px]" style={{ color: "var(--avoid-text)" }}>{issue.action_note}</div>}

        {live && !closing && (
          <div className="flex flex-col gap-2">
            <div className="font-mono text-[9.5px] uppercase text-ink-faint">{issue.status === "fixing" ? "Try another fix" : "Fix it"}</div>
            <div className="flex flex-wrap gap-2">{fixes}</div>
          </div>
        )}
        {issue.resolution && !live && <div className="text-[12px] text-ink-dim">Told them: {issue.resolution}</div>}

        {live && closing && (
          <div className="flex flex-col gap-2 rounded-lg p-3" style={{ border: "1px solid var(--line)" }}>
            <div className="text-[12.5px] font-semibold">
              {closing === "resolve" ? "Mark it fixed? Everyone who reported it is told." : "Close it without a fix? Everyone who reported it is told."}
            </div>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value.slice(0, CLOSE_NOTE_MAX))}
              maxLength={CLOSE_NOTE_MAX}
              rows={2}
              placeholder={closing === "resolve" ? "What you did (optional) — they’ll see it" : "Why (optional) — they’ll see it"}
              aria-label="Note for the people who reported it"
              className="rounded-lg px-3 py-2 text-[12.5px]"
              style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
            />
            <div className="flex flex-wrap gap-2">
              <Button variant={closing === "resolve" ? "primary" : "danger"} className={big} onClick={() => { void close(); }} busy={busy === closing} disabled={!!busy}>
                {closing === "resolve" ? "Mark fixed and tell them" : "Close and tell them"}
              </Button>
              <Button variant="ghost" className={big} onClick={() => setClosing(null)} disabled={!!busy}>Cancel</Button>
            </div>
          </div>
        )}

        {error && <div className="text-[12px] font-medium" style={{ color: "var(--reject)" }} role="alert">{error}</div>}

        <div className="flex flex-wrap gap-2">
          {live && !closing && (
            <>
              <Button className={big} onClick={() => setClosing("resolve")} disabled={!!busy}>Mark fixed…</Button>
              <Button className={big} onClick={() => setClosing("dismiss")} disabled={!!busy}>Close without a fix…</Button>
            </>
          )}
          {lib && <Link to={lib} className={`${big} inline-flex items-center rounded-lg px-4 text-[12.5px] font-semibold`} style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>Open title</Link>}
          <Button variant="ghost" className={`${big} ml-auto`} onClick={onClose} disabled={!!busy}>Close</Button>
        </div>
      </div>
    </Sheet>
  );
}
