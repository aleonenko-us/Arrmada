import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api, req } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { card, cardStyle } from "./shared";

// The Overview's needs-attention board: every missing file language grouped by why the
// last try gave up, each group with the one click that fixes it, and what the existing
// subtitles came from. The calls live in the Subtitles chunk, not lib/api.ts.

interface AttentionItem {
  title: string; kind: "movie" | "episode"; movie_id?: number; series_id?: number; season?: number; episode?: number;
  media_key: string; lang: string; variant?: string; detail?: string; at?: number; next_retry_at?: number; orphans?: string[];
}
interface AttentionBucket { reason: string; count: number; items: AttentionItem[]; fix?: "creds" | "download_large" | "retry" | "try_ai" | "adopt"; reset_at?: number }
interface AttentionBoard { buckets: AttentionBucket[]; total: number; sources: Record<string, number>; untracked: number; scanned: boolean }

const boardApi = {
  get: () => req<AttentionBoard>("/api/v1/subtitles/attention"),
  retry: (bucket: string, mode?: "ai") =>
    req<{ queued: number }>("/api/v1/subtitles/retry", { method: "POST", body: JSON.stringify({ all_in_bucket: bucket, mode }) }),
};

// What each bucket says, as a headline over its files.
const HEADLINE: Record<string, string> = {
  auth_failed: "OpenSubtitles login failed", quota: "Daily OpenSubtitles quota used", rate_limited: "OpenSubtitles is rate-limiting",
  provider_error: "OpenSubtitles errors", no_match: "No OpenSubtitles match", below_score: "Only poor matches on OpenSubtitles",
  out_of_sync: "Only out-of-sync downloads", model_missing: "Waiting for a whisper model", ai_failed: "AI generation failed",
  impossible: "Whisper can't make these from the audio", no_audio: "No audio to transcribe", ocr_needed: "Only image subtitles",
  orphaned: "Covered only by an orphaned subtitle",
};
const SOURCE_NAME: Record<string, string> = {
  embedded: "embedded", release: "from the release", opensubtitles: "OpenSubtitles", "ai-transcribe": "AI", "ai-translate": "AI translated",
  adopted: "adopted", manual: "chosen by hand", unknown: "checked, origin unknown",
};

function when(unix?: number): string {
  if (!unix) return "";
  return new Date(unix * 1000).toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

export function AttentionCard({ flash, onLibrary, onQueued }: { flash: (m: string) => void; onLibrary: () => void; onQueued: () => void }) {
  const [board, setBoard] = useState<AttentionBoard | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => boardApi.get().then(setBoard).catch(() => setBoard(null)), []);
  useEffect(() => { load(); }, [load]);

  const retry = async (b: AttentionBucket, mode?: "ai") => {
    setBusy(b.reason + (mode ?? ""));
    try {
      const r = await boardApi.retry(b.reason, mode);
      flash(r.queued ? `Queued ${r.queued} file${r.queued === 1 ? "" : "s"}${mode ? " for the AI" : ""}` : "Nothing left to retry");
      onQueued();
      await load();
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };
  const download = async (b: AttentionBucket) => {
    const large = b.items.some((it) => (it.detail ?? "").includes("large-v3"));
    setBusy(b.reason);
    try {
      await api.subtitleDownloadModel(large ? "ggml-large-v3.bin" : "ggml-large-v3-turbo.bin");
      flash("Downloading — the waiting files are tried again when it finishes");
    } catch (e) { flash((e as Error).message); } finally { setBusy(null); }
  };

  if (!board) return null;
  const sources = Object.entries(board.sources ?? {}).filter(([, n]) => n > 0).sort((a, b) => b[1] - a[1]);
  const btn = "rounded-md px-2.5 py-1 text-[11px] font-semibold disabled:opacity-50";
  const accent = { border: "1px solid var(--accent-line)", color: "var(--accent)" };
  const plain = { border: "1px solid var(--line)", color: "var(--ink-dim)" };

  return (
    <div className={card} style={cardStyle}>
      <div className="flex items-center justify-between">
        <div className="text-[14px] font-bold">Needs attention{board.total > 0 && <span className="ml-1.5 font-mono text-[11px] text-ink-faint">{board.total.toLocaleString()}</span>}</div>
      </div>
      {board.buckets.length === 0 ? (
        <div className="mt-1.5 text-[12px] text-ink-dim">Nothing is stuck: every missing subtitle is queued or will be found by the next sweep.</div>
      ) : (
        <div className="mt-2 flex flex-col gap-2">
          {board.buckets.map((b) => (
            <Bucket key={b.reason} b={b}>
              {b.fix === "creds" && <Link to={LINKS.subtitlesSettings} className={btn} style={accent}>Fix credentials</Link>}
              {b.fix === "download_large" && <button type="button" className={btn} style={accent} disabled={busy !== null} onClick={() => download(b)}>{b.items.some((it) => (it.detail ?? "").includes("large-v3")) ? "Download large-v3" : "Download a model"}</button>}
              {b.fix === "try_ai" && <button type="button" className={btn} style={accent} disabled={busy !== null} onClick={() => retry(b, "ai")}>{busy === b.reason + "ai" ? "Queuing…" : "Try AI now"}</button>}
              {(b.fix === "retry" || b.fix === "try_ai") && <button type="button" className={btn} style={b.fix === "retry" ? accent : plain} disabled={busy !== null} onClick={() => retry(b)}>{busy === b.reason ? "Queuing…" : "Retry now"}</button>}
              {b.fix === "adopt" && <button type="button" className={btn} style={accent} onClick={onLibrary}>Adopt / Replace</button>}
            </Bucket>
          ))}
        </div>
      )}
      {(sources.length > 0 || board.untracked > 0) && (
        <div className="mt-3 border-t pt-2.5 text-[11.5px] text-ink-faint" style={{ borderColor: "var(--line-soft)" }}>
          Subtitles by source: {sources.map(([s, n]) => `${SOURCE_NAME[s] ?? s} ${n.toLocaleString()}`).join(" · ")}
          {board.untracked > 0 && <>{sources.length > 0 ? " · " : ""}movies with subtitles Arrmada didn't make {board.untracked.toLocaleString()}</>}
        </div>
      )}
    </div>
  );
}

function Bucket({ b, children }: { b: AttentionBucket; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const shown = open ? b.items : b.items.slice(0, 3);
  const tone = b.reason === "auth_failed" ? "var(--reject-text)" : b.fix === "creds" || b.fix === "download_large" || b.reason === "ai_failed" ? "var(--avoid-text)" : "var(--ink)";
  let headline = `${HEADLINE[b.reason] ?? b.reason.replace(/_/g, " ")} — ${b.count.toLocaleString()} file${b.count === 1 ? "" : "s"} waiting`;
  if (b.reason === "quota" && b.reset_at) headline = `Daily OpenSubtitles quota used — resumes ${when(b.reset_at)} · ${b.count.toLocaleString()} waiting`;
  return (
    <div className="rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-[12.5px] font-semibold" style={{ color: tone }}>{headline}</span>
        <span className="flex flex-wrap items-center gap-1.5">{children}</span>
      </div>
      <ul className="mt-1.5 flex flex-col gap-0.5 text-[11.5px] text-ink-dim">
        {shown.map((it) => (
          <li key={it.media_key + it.lang} className="flex min-w-0 items-baseline gap-1.5">
            <Link to={it.kind === "movie" ? `/movies/${it.movie_id}` : `/series/${it.series_id}`} className="truncate font-semibold hover:underline" style={{ color: "var(--ink)" }}>{it.title}</Link>
            <span className="flex-none font-mono text-[9.5px] font-bold uppercase text-ink-faint">{it.lang}</span>
            {it.detail && <span className="truncate text-ink-faint" title={it.detail}>{it.detail}</span>}
            {!!it.next_retry_at && <span className="flex-none text-ink-faint">· next try {when(it.next_retry_at)}</span>}
          </li>
        ))}
      </ul>
      {b.items.length > 3 && (
        <button type="button" onClick={() => setOpen((v) => !v)} className="mt-1 text-[11px] text-ink-faint underline hover:text-[var(--ink)]">
          {open ? "Show fewer" : `Show ${b.items.length - 3} more${b.count > b.items.length ? ` (of ${b.count.toLocaleString()})` : ""}`}
        </button>
      )}
    </div>
  );
}
