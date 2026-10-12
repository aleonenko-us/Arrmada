import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Button, IconButton, Sheet, useConfirm, useToast } from "../ui";
import { usePoll } from "../lib/usePoll";
import { formatAgo, formatBytes } from "../lib/format";
import { langName, useLanguages } from "../lib/languages";
import { subMediaKey, subtitleFileApi, type SubFileView, type SubMediaRef, type SubSidecar } from "../lib/subtitleFileApi";
import { orphanApi } from "../pages/subtitles/orphanApi";
import { REASON_LABEL, SOURCE_LABEL, WantChip, sidecarForLang } from "./subtitles/WantChip";

// SubtitleDrawer is one video's subtitles in full (SUB-27): each kept language's state,
// every subtitle file beside the video with where it came from, its Health and its first
// lines, timing nudges, and Delete (to the recycle bin); orphans; the embedded tracks; and
// what the last attempts said. A right-hand panel on a desktop, a full-height sheet on a
// phone. Staff-only; LazySubtitleDrawer loads it on first open.

export interface SubtitleDrawerProps {
  media: SubMediaRef;
  /** Shown until the file view loads. */
  title?: string;
  onClose: () => void;
  /** Something on disk changed (a shift, a delete, an adopt): the opener can refresh. */
  onChanged?: () => void;
}

const lbl = "font-mono text-[9.5px] font-bold uppercase tracking-[0.11em] text-ink-faint";
const RUNG_LABEL: Record<string, string> = { extract: "Embedded", opensubtitles: "OpenSubtitles", ai: "AI", upgrade: "Upgrade", repair: "Repair" };
const NUDGES = [-1000, -500, 500, 1000];

// fmtCue is a cue time as h:mm:ss.mmm (the hour only when there is one).
export function fmtCue(ms: number): string {
  const neg = ms < 0;
  let t = Math.abs(Math.round(ms));
  const h = Math.floor(t / 3_600_000); t -= h * 3_600_000;
  const m = Math.floor(t / 60_000); t -= m * 60_000;
  const s = Math.floor(t / 1000);
  const frac = String(t - s * 1000).padStart(3, "0");
  const mmss = `${String(m).padStart(h ? 2 : 1, "0")}:${String(s).padStart(2, "0")}.${frac}`;
  return `${neg ? "−" : ""}${h ? `${h}:` : ""}${mmss}`;
}

// fmtShift is a signed shift in seconds: "+1.5 s", "−0.5 s".
export function fmtShift(ms: number): string {
  const s = (Math.abs(ms) / 1000).toFixed(1).replace(/\.0$/, "");
  return `${ms < 0 ? "−" : "+"}${s} s`;
}

export default function SubtitleDrawer({ media, title, onClose, onChanged }: SubtitleDrawerProps) {
  const [view, setView] = useState<SubFileView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const languages = useLanguages();
  const toast = useToast();

  // Keyed on the file, not the object: a caller passing a fresh literal each render
  // mustn't refetch.
  const key = subMediaKey(media);
  const load = useCallback(() => subtitleFileApi.view(media).then((v) => { setView(v); setError(null); }).catch((e: Error) => setError(e.message)),
    [key]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { void load(); }, [load]);
  // While a job works on the file, follow it: its sidecars appear as they're written.
  const working = (view?.jobs.length ?? 0) > 0;
  usePoll(load, working ? 4000 : null, { immediate: false });

  const changed = useCallback(() => { void load(); onChanged?.(); }, [load, onChanged]);
  const name = (code: string) => (code ? `${langName(code, languages)}` : "untagged");

  const heading = view?.title || title || "Subtitles";
  const file = view?.video_path.split(/[\\/]/).pop();

  return (
    <Sheet
      onClose={onClose}
      ariaLabel={`Subtitles — ${heading}`}
      handle={false}
      panelClassName="thin-scroll w-full overflow-y-auto rounded-t-2xl pb-[env(safe-area-inset-bottom)] h-[calc(100dvh-0.75rem)] sm:ml-auto sm:h-auto sm:max-w-[600px] sm:self-stretch sm:rounded-2xl sm:pb-0"
    >
      <div className="sticky top-0 z-[1] flex items-start gap-3 px-4 pb-3 pt-4" style={{ background: "var(--panel)", borderBottom: "1px solid var(--line-soft)" }}>
        <div className="min-w-0 flex-1">
          <div className={lbl}>Subtitles</div>
          <h2 className="m-0 mt-0.5 truncate text-[15px] font-bold" title={heading}>{heading}</h2>
          {file && <div className="mt-0.5 truncate font-mono text-[10.5px] text-ink-faint" title={view?.video_path}>{file}</div>}
        </div>
        <IconButton label="Close" onClick={onClose} className="h-8 w-8 flex-none rounded-lg text-[16px] text-ink-dim hover:bg-[var(--panel-2)]">✕</IconButton>
      </div>

      <div className="flex flex-col gap-5 px-4 py-4">
        {error && !view && <div className="rounded-lg p-3 text-[12.5px]" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>{error}</div>}
        {!view && !error && <div className="py-10 text-center text-[12.5px] text-ink-dim">Reading the folder…</div>}
        {view && (
          <>
            {view.jobs.length > 0 && (
              <div className="rounded-lg px-3 py-2 text-[12px]" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)", color: "var(--accent-text)" }}>
                {view.jobs.map((j) => (
                  <div key={j.id}>{j.state === "running" ? `Working: ${j.stage || "starting"}${j.progress ? ` · ${j.progress}%` : ""}` : `Queued${j.lane === "gpu" ? " for AI" : ""}`}</div>
                ))}
              </div>
            )}

            <Section label="Languages you keep">
              <div className="flex flex-wrap gap-1.5">
                {view.languages.map((l, i) => <WantChip key={l.lang} l={l} sidecar={sidecarForLang(l.lang, view.sidecars, i === 0)} />)}
              </div>
            </Section>

            <Section label={`Subtitle files · ${view.sidecars.length}`}>
              {view.sidecars.length === 0 ? (
                <p className="m-0 text-[12px] text-ink-dim">No subtitle files beside this video yet.</p>
              ) : (
                <div className="flex flex-col gap-2.5">
                  {view.sidecars.map((s) => <SidecarCard key={s.name} media={media} s={s} langLabel={name(s.lang)} onChanged={changed} toast={toast} />)}
                </div>
              )}
            </Section>

            {view.orphans.length > 0 && (
              <Section label="Not linked to this file" hint="Named for no video in this folder, so Plex won't show them with this file.">
                <div className="flex flex-col gap-1.5">
                  {view.orphans.map((o) => <OrphanRow key={o.name} media={media} name={o.name} lang={o.lang} onChanged={changed} toast={toast} />)}
                </div>
              </Section>
            )}

            <Section label={`Embedded tracks · ${view.embedded.length}`} hint="Inside the video. Text tracks can be copied out as subtitle files; image tracks need OCR.">
              {view.embedded.length === 0 ? (
                <p className="m-0 text-[12px] text-ink-dim">None.</p>
              ) : (
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {view.embedded.map((t) => (
                    <li key={t.index} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px]">
                      <span className="font-mono text-[10.5px] text-ink-faint">#{t.index}</span>
                      <span className="font-semibold">{t.lang && t.lang !== "und" ? name(t.lang) : "No language"}</span>
                      {t.title && <span className="truncate text-ink-dim">“{t.title}”</span>}
                      <Tag>{t.text ? t.codec : `${t.codec} · image`}</Tag>
                      {t.forced && <Tag>forced</Tag>}
                      {t.sdh && <Tag>SDH</Tag>}
                      {t.default && <Tag>default</Tag>}
                    </li>
                  ))}
                </ul>
              )}
            </Section>

            {view.audio.length > 0 && (
              <Section label={`Audio · ${view.audio.length}`}>
                <div className="flex flex-wrap gap-1.5 text-[12px] text-ink-dim">
                  {view.audio.map((a) => <Tag key={a.index}>{`#${a.index} ${a.lang ? name(a.lang) : "untagged"}${a.default ? " · default" : ""}`}</Tag>)}
                </div>
              </Section>
            )}

            {view.attempts.length > 0 && (
              <Section label="Last attempts">
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {view.attempts.map((a) => (
                    <li key={a.id} className="flex flex-wrap items-baseline gap-x-2 text-[11.5px]">
                      <span className="font-mono text-[10.5px] font-bold uppercase">{a.lang}{a.variant ? `.${a.variant}` : ""}</span>
                      <span className="text-ink-faint">{RUNG_LABEL[a.rung] ?? a.rung}</span>
                      <span style={{ color: a.outcome === "ok" ? "var(--good)" : "var(--ink-dim)" }}>{a.detail || REASON_LABEL[a.outcome] || a.outcome}</span>
                      <span className="ml-auto font-mono text-[10px] text-ink-faint">{formatAgo(a.at * 1000)}</span>
                    </li>
                  ))}
                </ul>
              </Section>
            )}
          </>
        )}
      </div>
    </Sheet>
  );
}

function Section({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <section>
      <div className={lbl}>{label}</div>
      {hint && <p className="m-0 mt-0.5 text-[11px] text-ink-faint">{hint}</p>}
      <div className="mt-2">{children}</div>
    </section>
  );
}

function Tag({ children }: { children: ReactNode }) {
  return <span className="rounded px-1.5 py-0.5 font-mono text-[10px]" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>{children}</span>;
}

// SidecarCard is one subtitle file: language and source, Health, where it came from, its
// first three lines, the timing nudges, and Delete.
function SidecarCard({ media, s, langLabel, onChanged, toast }: {
  media: SubMediaRef; s: SubSidecar; langLabel: string; onChanged: () => void; toast: ReturnType<typeof useToast>;
}) {
  const confirm = useConfirm();
  const [pending, setPending] = useState(0);
  const [busy, setBusy] = useState<null | "shift" | "delete">(null);
  const health = s.sync_confidence;

  const apply = async () => {
    setBusy("shift");
    try {
      await subtitleFileApi.shift(media, s.name, pending);
      toast(`Shifted ${s.name} by ${fmtShift(pending)}`);
      setPending(0);
      onChanged();
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(null); }
  };
  const remove = async () => {
    const ok = await confirm({
      title: `Delete ${s.name}?`,
      body: <>It goes to the recycle bin, so you can restore it from there.{s.provider_file_id ? " This download won't be fetched again for this file." : ""} The next run may make this language again from another source.</>,
      confirmLabel: "Move to recycle bin",
      tone: "danger",
    });
    if (!ok) return;
    setBusy("delete");
    try {
      await subtitleFileApi.remove(media, s.name);
      toast(`${s.name} is in the recycle bin`);
      onChanged();
    } catch (e) { toast((e as Error).message, { tone: "error" }); setBusy(null); }
  };

  const provenance = [
    s.release,
    s.hash_match ? "hash match" : s.score != null ? `score ${s.score}` : "",
    s.model,
    s.embedded_index != null ? `track #${s.embedded_index}` : "",
    s.sync_offset_ms ? `shifted ${fmtShift(s.sync_offset_ms)}` : "",
  ].filter(Boolean);

  return (
    <div className="rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[12.5px] font-semibold">{langLabel}</span>
        {s.variant === "sdh" && <Tag>SDH</Tag>}
        {s.variant === "forced" && <Tag>forced</Tag>}
        <span className="rounded px-1.5 py-0.5 text-[10px] font-semibold" style={{ background: s.owned ? "var(--accent-soft)" : "var(--panel)", color: s.owned ? "var(--accent-text)" : "var(--ink-dim)", border: "1px solid var(--line-soft)" }}>
          {SOURCE_LABEL[s.source] ?? s.source}
        </span>
        <span
          className="rounded px-1.5 py-0.5 font-mono text-[10px]"
          style={{ color: health == null ? "var(--ink-faint)" : health >= 70 ? "var(--good)" : health >= 50 ? "var(--accent)" : "var(--avoid)", border: "1px solid var(--line-soft)" }}
          title={health == null ? "Its timing hasn't been checked against the audio" : "How well its timing fits the speech in the audio (0-100)"}
        >
          {health == null ? "Health not checked" : `Health ${health}`}
        </span>
        {s.locked && <Tag>kept</Tag>}
        <span className="ml-auto font-mono text-[10px] text-ink-faint">{formatBytes(s.size)}</span>
      </div>
      <div className="mt-1 break-all font-mono text-[10.5px] text-ink-faint">{s.name}</div>
      {provenance.length > 0 && <div className="mt-0.5 text-[11px] text-ink-dim">{provenance.join(" · ")}</div>}

      {s.first_cues.length > 0 ? (
        <ol className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
          {s.first_cues.map((c, i) => (
            <li key={i} className="flex gap-2 text-[12px] leading-snug">
              <span className="w-[86px] flex-none font-mono text-[10.5px]" style={{ color: pending ? "var(--accent)" : "var(--ink-faint)" }}>{fmtCue(Math.max(0, c.start_ms + pending))}</span>
              <span className="min-w-0 flex-1">{c.text}</span>
            </li>
          ))}
          {s.cue_count > s.first_cues.length && <li className="font-mono text-[10px] text-ink-faint">… {s.cue_count} lines in all</li>}
        </ol>
      ) : (
        <p className="m-0 mt-2 text-[11.5px] text-ink-faint">{s.ext === ".srt" ? "No lines could be read." : "No preview for this format."}</p>
      )}

      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        {s.shiftable && (
          <>
            <span className="mr-0.5 text-[11px] text-ink-faint">Timing</span>
            {NUDGES.map((n) => (
              <Button key={n} size="sm" onClick={() => setPending((p) => p + n)} disabled={busy !== null} aria-label={`Move ${fmtShift(n)}`}>{fmtShift(n)}</Button>
            ))}
            {pending !== 0 && (
              <>
                <Button size="sm" variant="primary" onClick={apply} busy={busy === "shift"} busyLabel="Applying…">Apply {fmtShift(pending)}</Button>
                <Button size="sm" variant="ghost" onClick={() => setPending(0)} disabled={busy !== null}>Reset</Button>
              </>
            )}
          </>
        )}
        <Button size="sm" variant="ghost" className="ml-auto" onClick={remove} busy={busy === "delete"} busyLabel="Deleting…" disabled={busy !== null || pending !== 0} style={{ color: "var(--reject)" }}>Delete…</Button>
      </div>
    </div>
  );
}

// OrphanRow is a subtitle named for no video: Adopt (movies) renames it to pair with
// this file; Delete sends it to the recycle bin.
function OrphanRow({ media, name, lang, onChanged, toast }: { media: SubMediaRef; name: string; lang?: string; onChanged: () => void; toast: ReturnType<typeof useToast> }) {
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<string>) => {
    setBusy(true);
    try { toast(await fn()); onChanged(); } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  const adopt = () => run(async () => {
    if (media.kind !== "movie") return "";
    const r = await orphanApi.adopt(media.movie_id, name);
    return `Adopted — now ${r.name}`;
  });
  const remove = async () => {
    if (!(await confirm({ title: `Delete ${name}?`, body: "It goes to the recycle bin, so you can restore it from there.", confirmLabel: "Move to recycle bin", tone: "danger" }))) return;
    await run(async () => { await subtitleFileApi.remove(media, name); return `${name} is in the recycle bin`; });
  };
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[11.5px]">
      <span className="min-w-0 flex-1 truncate font-mono text-ink-dim" title={name}>{name}</span>
      {lang && <Tag>{lang}</Tag>}
      {media.kind === "movie" && <Button size="sm" onClick={adopt} disabled={busy} title="Rename it to pair with this file, so Plex shows it" style={{ border: "1px solid var(--accent-line)", color: "var(--accent)", background: "transparent" }}>Adopt</Button>}
      <Button size="sm" variant="ghost" onClick={remove} disabled={busy} style={{ color: "var(--reject)" }}>Delete…</Button>
    </div>
  );
}
