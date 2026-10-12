import { useCallback, useEffect, useState } from "react";
import { Button, useConfirm, useToast } from "../../ui";
import { subtitleFileApi, type SubCandidate, type SubMediaRef, type SubSearchView } from "../../lib/subtitleFileApi";

// SearchPanel is the drawer's "Search OpenSubtitles" for one language (SUB-28): every
// candidate scored against the file, best first, with why (hover the score), the HI /
// hash / frame-rate facts and Use this. A search costs no download but does cost the
// API's rate limit, so it runs once when the panel opens and again only on Refresh.
// "Use this" downloads that file, sync-checks it (a confident correction is applied, a
// doubtful one only warned about), replaces the current subtitle and keeps the choice:
// the automatic upgrades never replace it.

// quotaLine says what a download would cost: "12 downloads left today", or when a spent
// quota comes back.
export function quotaLine(v: SubSearchView, now = Date.now()): { text: string; spent: boolean } {
  if (!v.can_download) return { text: "Downloading needs the OpenSubtitles username and password (Settings → Connections).", spent: true };
  if (v.quota_reset_at > now / 1000) {
    const at = new Date(v.quota_reset_at * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
    return { text: `Today's downloads are used up — resets ${at}.`, spent: true };
  }
  if (v.quota_remaining >= 0) return { text: `${v.quota_remaining} download${v.quota_remaining === 1 ? "" : "s"} left today. Searching is free.`, spent: false };
  return { text: "Searching is free; Use this spends one download.", spent: false };
}

export function SearchPanel({ media, lang, langLabel, onQueued }: { media: SubMediaRef; lang: string; langLabel: string; onQueued: () => void }) {
  const [view, setView] = useState<SubSearchView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [picking, setPicking] = useState<string | null>(null);
  const confirm = useConfirm();
  const toast = useToast();

  const run = useCallback(async (refresh: boolean) => {
    setLoading(true);
    try { setView(await subtitleFileApi.search(media, lang, refresh)); setError(null); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, [media, lang]);
  // Once per open (the server keeps the answer for a few minutes too); Refresh asks again.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void run(false); }, [lang]);

  const quota = view ? quotaLine(view) : null;
  const pick = async (c: SubCandidate) => {
    const ok = await confirm({
      title: `Use ${c.release || c.file_name || "this subtitle"}?`,
      body: <>It's downloaded (one of today's OpenSubtitles downloads), checked against the audio — moved into sync when the check is sure, flagged when it isn't — and saved as the {langLabel} subtitle. The one there now goes to the recycle bin once the new one is written. Arrmada keeps your choice: automatic upgrades won't replace it.</>,
      confirmLabel: "Use this",
    });
    if (!ok) return;
    setPicking(c.file_id);
    try {
      await subtitleFileApi.redo(media, { lang, mode: "pick", file_id: c.file_id });
      toast(`Queued: ${lang} from ${c.release || c.file_name || "the chosen file"}`);
      onQueued();
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setPicking(null); }
  };

  return (
    <div className="mt-2 rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }} aria-label={`OpenSubtitles results for ${langLabel}`} role="region">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[12px] font-semibold">OpenSubtitles · {langLabel}</span>
        {quota && <span className="text-[11px]" style={{ color: quota.spent ? "var(--avoid)" : "var(--ink-faint)" }}>{quota.text}</span>}
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => run(true)} busy={loading} busyLabel="Searching…">Refresh</Button>
      </div>
      {error && <p className="m-0 mt-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</p>}
      {!view && !error && <p className="m-0 mt-2 text-[12px] text-ink-dim">Searching…</p>}
      {view && view.candidates.length === 0 && <p className="m-0 mt-2 text-[12px] text-ink-dim">Nothing on OpenSubtitles for this file in {langLabel}.</p>}
      {view && view.candidates.length > 0 && (
        <ol className="m-0 mt-2 flex list-none flex-col gap-1.5 p-0">
          {view.candidates.map((c) => {
            const usable = !c.rejected && !c.blocklisted;
            const current = c.file_id === view.current_file_id;
            const below = !c.hash_match && c.score < view.min_score;
            return (
              <li key={c.file_id} className="rounded-md px-2.5 py-2 text-[12px]" style={{ background: "var(--panel)", border: `1px solid ${current ? "var(--accent-line)" : "var(--line-soft)"}`, opacity: usable ? 1 : 0.65 }}>
                <div className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="break-all font-semibold">{c.release || c.file_name || `file ${c.file_id}`}</div>
                    {c.file_name && c.release && c.file_name !== c.release && <div className="break-all font-mono text-[10px] text-ink-faint">{c.file_name}</div>}
                  </div>
                  <span
                    className="flex-none rounded px-1.5 py-0.5 font-mono text-[11px] font-bold"
                    style={{ background: c.hash_match ? "var(--good-soft, rgba(127,176,105,.16))" : "var(--panel-2)", color: c.hash_match ? "var(--good)" : below ? "var(--avoid)" : "var(--ink)" }}
                    title={(c.reasons.length ? c.reasons.join("\n") : "Nothing in the release name matched this file") + (below ? `\nBelow the minimum score (${view.min_score}) the automatic download needs` : "")}
                  >
                    {c.score}
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-1.5 font-mono text-[10px] text-ink-dim">
                  {c.hash_match && <span style={{ color: "var(--good)" }}>hash match</span>}
                  {c.hearing_impaired && <span>HI</span>}
                  {c.fps ? <span>{Math.round(c.fps * 1000) / 1000} fps</span> : null}
                  <span>{c.downloads.toLocaleString()} downloads</span>
                  {c.trusted && <span>trusted</span>}
                  {current && <span style={{ color: "var(--accent)" }}>in use</span>}
                  {c.blocklisted && <span style={{ color: "var(--avoid)" }}>blocked for this file</span>}
                  {c.rejected && <span style={{ color: "var(--avoid)" }}>{c.rejected}</span>}
                  <span className="ml-auto">
                    <Button
                      size="sm"
                      variant={c.hash_match ? "primary" : "secondary"}
                      onClick={() => pick(c)}
                      busy={picking === c.file_id}
                      busyLabel="Queuing…"
                      disabled={!usable || current || picking !== null || !!quota?.spent}
                      title={!usable ? "Can't be used for this file" : current ? "Already the subtitle in use" : quota?.spent ? quota.text : "Download this one and make it the subtitle"}
                    >
                      Use this
                    </Button>
                  </span>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}
