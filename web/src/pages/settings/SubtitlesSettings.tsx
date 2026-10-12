import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { AutoNumber, SavedTick, wholeNumber } from "../../components/settings/auto";
import { useScrollWhenLoaded } from "../../components/settings/ui";
import { api, type SubtitleSettings, type WhisperStatus } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { useAutoSave, type AutoSave } from "../../lib/useAutoSave";
import { usePoll } from "../../lib/usePoll";
import { useToast } from "../../ui";
import { KeptTracksNote, LANGS, card, cardStyle } from "../subtitles/shared";

// Settings → Subtitles & Convert → Subtitles: which languages are kept, whether new files
// get them automatically, the local AI models, and where OpenSubtitles stands. It used to
// be the Subtitles page's Settings tab; that page now links here (Configure →). Each
// control saves itself.
export const SUBTITLES_SETTINGS_ID = "subtitles";

export function SubtitlesSettings() {
  const [settings, setSettings] = useState<SubtitleSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.subtitleSettings().then(setSettings).catch((e: Error) => setError(e.message)); }, []);
  useScrollWhenLoaded(SUBTITLES_SETTINGS_ID, !!settings);
  return (
    <div id={SUBTITLES_SETTINGS_ID} className="flex scroll-mt-20 flex-col gap-4">
      <h2 className="m-0 text-[15px] font-bold">Subtitles</h2>
      {settings ? <SubtitlesForm settings={settings} onSaved={setSettings} /> : <p className="m-0 text-[12.5px] text-ink-dim">{error ?? "Loading…"}</p>}
    </div>
  );
}

const sameLangs = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

function SubtitlesForm({ settings, onSaved }: { settings: SubtitleSettings; onSaved: (s: SubtitleSettings) => void }) {
  const save = (body: { movies_auto?: boolean; series_auto?: boolean; languages?: string[]; min_score?: number }) => api.updateSubtitleSettings(body).then(onSaved);
  const langs = useAutoSave(settings.languages, (v) => save({ languages: v }), {
    equal: sameLangs,
    keepOnError: false,
    validate: (v) => (v.length === 0 ? "Keep at least one language." : null),
  });
  const movies = useAutoSave(settings.movies_auto, (v) => save({ movies_auto: v }));
  const series = useAutoSave(settings.series_auto, (v) => save({ series_auto: v }));
  const minScore = useAutoSave(String(settings.min_score ?? 60), (v) => save({ min_score: Number(v) }), {
    validate: wholeNumber(0, 100, "The minimum match score"),
  });
  const toggleLang = (code: string) => {
    const has = langs.value.includes(code);
    void langs.commit(has ? langs.value.filter((l) => l !== code) : [...langs.value, code]);
  };
  return (
    <>
      <div className={card} style={cardStyle}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-[14px] font-bold">Kept languages</div>
          <SavedTick save={langs} />
        </div>
        <div className="mt-0.5 text-[11.5px] text-ink-faint">Arrmada keeps an external <code>.srt</code> for each of these. Click to toggle. <KeptTracksNote /></div>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {LANGS.map((l) => {
            const on = langs.value.includes(l.code);
            return <button key={l.code} aria-pressed={on} onClick={() => toggleLang(l.code)} className="rounded-full px-2.5 py-1 text-[11.5px] font-semibold" style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel-2)", color: on ? "var(--accent)" : "var(--ink-faint)" }}>{l.name}</button>;
          })}
        </div>
      </div>

      <div className={card} style={cardStyle}>
        <div className="text-[14px] font-bold">Automatic</div>
        <div className="mt-0.5 text-[11.5px] text-ink-faint">When on, every new download gets its subtitles as soon as it imports, and a sweep every six hours catches anything still missing (including releases whose subtitles appeared later). Off = run it yourself from the Library.</div>
        <div className="mt-3 flex flex-col gap-2.5">
          <ToggleRow label="Auto-ensure movies" save={movies} />
          <ToggleRow label="Auto-ensure series" save={series} />
        </div>
      </div>

      <LocalAI backend={settings.ai_backend} note={settings.ai_note} />

      <div className={card} style={cardStyle}>
        <div className="text-[14px] font-bold">OpenSubtitles (optional download source)</div>
        <div className="mt-0.5 text-[11.5px] text-ink-faint">A download source Arrmada tries before AI. Optional — embedded extraction and local AI work without it.</div>
        <div className="mt-3 flex items-center gap-2 text-[12px]">
          <span className="rounded-full px-2 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: settings.can_download ? "var(--good-soft, rgba(127,176,105,.16))" : settings.provider_ready ? "var(--avoid-soft)" : "var(--panel-2)", color: settings.can_download ? "var(--good)" : settings.provider_ready ? "var(--avoid)" : "var(--ink-faint)" }}>
            {settings.can_download ? "ready" : settings.provider_ready ? "search only" : "not configured"}
          </span>
          <span className="text-ink-dim">
            {settings.can_download
              ? settings.quota_reset_at > 0
                ? `Daily download quota used up — resumes ${new Date(settings.quota_reset_at * 1000).toLocaleString()}.`
                : settings.quota_remaining >= 0
                  ? `Downloading enabled · ${settings.quota_remaining} downloads left today.`
                  : "Downloading enabled."
              : settings.provider_ready
                ? <>Searching works; add your OpenSubtitles username and password under <Link to={LINKS.apiKeys} style={{ color: "var(--accent)" }}>Settings → Connections</Link> to download.</>
                : <>Add an OpenSubtitles API key (free) under <Link to={LINKS.apiKeys} style={{ color: "var(--accent)" }}>Settings → Connections</Link>, plus your account username and password to download.</>}
          </span>
        </div>
        {/* Advanced: how close a download's release must be to the file when it wasn't
            uploaded for this exact file. Read per job, so a change applies to the next one. */}
        <div className="mt-3 border-t pt-3" style={{ borderColor: "var(--line)" }}>
          <AutoNumber label="Minimum match score" save={minScore} min={0} max={100} placeholder="60" className="w-24 rounded-lg px-3 py-2 font-mono text-[12.5px]">
            <span className="text-[10.5px] text-ink-faint">Advanced. A subtitle uploaded for this exact file is always taken. Any other is scored 0–100 on how closely its release matches the file (group, source, edition, frame rate), and one below this goes to the next source instead. Default 60.</span>
          </AutoNumber>
        </div>
      </div>
    </>
  );
}

function LocalAI({ backend, note }: { backend?: string; note?: string }) {
  const toast = useToast();
  const [status, setStatus] = useState<WhisperStatus | null>(null);
  const load = useCallback(() => api.subtitleModels().then(setStatus).catch(() => setStatus(null)), []);
  usePoll(load, 3000); // reflect download progress
  const download = async (name: string) => {
    try { await api.subtitleDownloadModel(name); toast("Downloading — progress shows in Subtitles → Logs.", { tone: "good" }); load(); }
    catch (e) { toast((e as Error).message, { tone: "error" }); }
  };
  const st = status;
  return (
    <div className={card} style={cardStyle}>
      <div className="flex items-center justify-between">
        <div className="text-[14px] font-bold">Local AI (whisper)</div>
        <span className="rounded-full px-2 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: st?.ready ? "var(--good-soft, rgba(127,176,105,.16))" : "var(--panel-2)", color: st?.ready ? "var(--good)" : "var(--ink-faint)" }}>
          {st?.ready ? "ready" : st?.binary_ready ? "needs a model" : "not installed"}
        </span>
      </div>
      <div className="mt-0.5 text-[11.5px] text-ink-faint">Generates subtitles from a file's audio when no better source exists — 100% local, no key. Download a model to enable it (large files, runs in the background).</div>
      {/* Which compute backend the last run used. A Vulkan build with no visible device runs
          on the CPU without complaint, so "ready" alone can hide a GPU that isn't being used. */}
      {st?.ready && (
        <div className="mt-1.5 text-[11.5px]" style={{ color: backend === "vulkan" || backend === "sycl" ? "var(--good)" : "var(--ink-dim)" }}>
          {backend === "sycl"
            ? "Running on the GPU (Intel oneAPI)."
            : backend === "vulkan"
            ? "Running on the GPU (Vulkan)."
            : backend === "cpu"
              ? "Running on the CPU. If this host has a GPU, check that /dev/dri is passed to the container and vulkaninfo inside it lists a device."
              : "Backend not known yet — it's reported after the first subtitle is generated."}
        </div>
      )}
      {/* The Intel oneAPI build is tried first; when it's set aside, say why rather than
          quietly running slower. */}
      {st?.ready && note && (
        <div className="mt-1 text-[11.5px]" style={{ color: "var(--avoid)" }}>Intel oneAPI build not in use — {note}</div>
      )}
      {st && !st.binary_ready && <div className="mt-2 text-[11.5px]" style={{ color: "var(--avoid)" }}>whisper-cli isn't in this build yet — update to a build that bundles it.</div>}
      <div className="mt-3 flex flex-col gap-2">
        {(st?.models ?? []).map((m) => (
          <div key={m.name} className="flex items-center justify-between gap-3 rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }}>
            <div className="min-w-0">
              <div className="text-[12px] font-semibold">{m.label}</div>
              <div className="font-mono text-[10px] text-ink-faint">{m.name} · ~{m.size_mb >= 1000 ? `${(m.size_mb / 1000).toFixed(1)} GB` : `${m.size_mb} MB`}</div>
            </div>
            {m.present ? (
              <span className="rounded px-2 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good)" }}>installed ✓</span>
            ) : m.downloading ? (
              <span className="rounded px-2 py-0.5 font-mono text-[9px] font-bold uppercase" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>downloading…</span>
            ) : (
              <button onClick={() => download(m.name)} disabled={!st?.binary_ready} className="rounded-lg px-3 py-1.5 text-[11px] font-semibold disabled:opacity-40" style={{ border: "1px solid var(--accent-line)", color: "var(--accent)" }}>Download</button>
            )}
          </div>
        ))}
      </div>
      <div className="mt-2 text-[10.5px] text-ink-faint">Get <b>turbo</b> for English, plus <b>large-v3</b> to translate foreign audio to English.</div>
    </div>
  );
}

function ToggleRow({ label, save }: { label: string; save: AutoSave<boolean> }) {
  const on = save.value;
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center justify-between gap-3">
        <span className="text-[12.5px] font-semibold">{label}</span>
        <button role="switch" aria-checked={on} aria-label={label} onClick={() => void save.commit(!on)} className="relative inline-flex h-6 w-11 flex-none items-center rounded-full transition-colors" style={{ background: on ? "var(--accent)" : "var(--panel-2)", border: "1px solid var(--line)" }}>
          <span className="inline-block h-4 w-4 rounded-full bg-white transition-transform" style={{ transform: on ? "translateX(22px)" : "translateX(3px)" }} />
        </button>
      </div>
      {save.status !== "idle" && <div className="flex justify-end"><SavedTick save={save} /></div>}
    </div>
  );
}
