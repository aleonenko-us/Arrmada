import { useCallback, useEffect, useId, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { KeyEditor, keyRowsFor, useAPIKeys } from "../../components/connections/KeyEditor";
import { AutoNumber, SavedTick, wholeNumber } from "../../components/settings/auto";
import { useScrollWhenLoaded } from "../../components/settings/ui";
import { api, type SubtitleProviderState, type SubtitleSettings, type WhisperStatus } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { isAdmin, useMe } from "../../lib/me";
import { useAutoSave, type AutoSave } from "../../lib/useAutoSave";
import { usePoll } from "../../lib/usePoll";
import { useToast } from "../../ui";
import { Switch } from "../../ui/Switch";
import { LanguagePicker } from "../../components/LanguagePicker";
import { KeptTracksNote, card, cardStyle } from "../subtitles/shared";
import { SubtitleProfiles } from "./SubtitleProfiles";

// Settings → Subtitles: which languages are kept, whether new files
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
      <h2 className="m-0 text-lg font-bold">Subtitles</h2>
      {settings ? <SubtitlesForm settings={settings} onSaved={setSettings} /> : <p className="m-0 text-base text-ink-dim">{error ?? "Loading…"}</p>}
    </div>
  );
}

const sameLangs = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

function SubtitlesForm({ settings, onSaved }: { settings: SubtitleSettings; onSaved: (s: SubtitleSettings) => void }) {
  const save = (body: { movies_auto?: boolean; series_auto?: boolean; languages?: string[]; min_score?: number; ai_pause_watching?: boolean }) => api.updateSubtitleSettings(body).then(onSaved);
  const langs = useAutoSave(settings.languages, (v) => save({ languages: v }), {
    equal: sameLangs,
    keepOnError: false,
    validate: (v) => (v.length === 0 ? "Keep at least one language." : null),
  });
  const movies = useAutoSave(settings.movies_auto, (v) => save({ movies_auto: v }));
  const series = useAutoSave(settings.series_auto, (v) => save({ series_auto: v }));
  const pauseWatching = useAutoSave(settings.ai_pause_watching ?? true, (v) => save({ ai_pause_watching: v }));
  const minScore = useAutoSave(String(settings.min_score ?? 60), (v) => save({ min_score: Number(v) }), {
    validate: wholeNumber(0, 100, "The minimum match score"),
  });
  return (
    <>
      <div className={card} style={cardStyle}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-md font-bold">Kept languages</div>
          <SavedTick save={langs} />
        </div>
        <div className="mt-0.5 text-sm text-ink-faint">Arrmada keeps an external <code>.srt</code> for each of these, in this order. <KeptTracksNote /></div>
        <div className="mt-3">
          <LanguagePicker
            value={langs.value}
            onChange={(next) => void langs.commit(next)}
            label="Add a kept language"
            firstNote="A subtitle file with no language in its name counts as this one."
          />
        </div>
      </div>

      <SubtitleProfiles version={settings.languages.join(",")} />

      <div className={card} style={cardStyle}>
        <div className="text-md font-bold">Automatic</div>
        <div className="mt-0.5 text-sm text-ink-faint">When on, every new download gets its subtitles as soon as it imports, and a sweep every six hours catches anything still missing (including releases whose subtitles appeared later). Off = run it yourself from the Library.</div>
        <div className="mt-3 flex flex-col gap-2.5">
          <ToggleRow label="Auto-ensure movies" save={movies} />
          <ToggleRow label="Auto-ensure series" save={series} />
        </div>
      </div>

      <LocalAI backend={settings.ai_backend} note={settings.ai_note} cpuOnly={settings.ai_cpu_only}>
        {/* AI subtitles share the box with Plex and Jellyfin: by default they wait while
            someone watches, stopping between chunks and carrying on afterwards. */}
        <div className="mt-3 border-t pt-3" style={{ borderColor: "var(--line)" }}>
          <ToggleRow label="Pause while someone is watching" save={pauseWatching} />
          <div className="mt-0.5 text-xs text-ink-faint">
            {settings.watching_known === false
              ? "Insights isn't monitoring Plex or Jellyfin, so Arrmada can't tell when someone is watching and this does nothing yet."
              : settings.ai_paused
              ? "Paused now — someone is watching. It carries on where it stopped when they finish."
              : "Keeps the server free for streams: a file being transcribed stops within a few minutes and carries on afterwards."}
          </div>
        </div>
      </LocalAI>

      <OpenSubtitlesCard settings={settings} onChanged={() => void api.subtitleSettings().then(onSaved).catch(() => {})}>
        {/* Advanced: how close a download's release must be to the file when it wasn't
            uploaded for this exact file. Read per job, so a change applies to the next one. */}
        <div className="mt-3 border-t pt-3" style={{ borderColor: "var(--line)" }}>
          <AutoNumber label="Minimum match score" save={minScore} min={0} max={100} placeholder="60" className="w-24 rounded-lg px-3 py-2 font-mono text-base">
            <span className="text-xs text-ink-faint">Advanced. A subtitle uploaded for this exact file is always taken. Any other is scored 0–100 on how closely its release matches the file (group, source, edition, frame rate), and one below this goes to the next source instead. Default 60.</span>
          </AutoNumber>
        </div>
      </OpenSubtitlesCard>
    </>
  );
}

// What OpenSubtitles last said, as the chip and the state line read it. Only "ok" means a
// login with these credentials has worked.
const PROVIDER_CHIP: Record<SubtitleProviderState, { label: string; tone: "good" | "avoid" | "reject" | null }> = {
  ok: { label: "logged in", tone: "good" },
  unverified: { label: "not checked", tone: "avoid" },
  needs_account: { label: "search only", tone: "avoid" },
  quota_spent: { label: "quota used", tone: "avoid" },
  rate_limited: { label: "rate-limited", tone: "avoid" },
  auth_failed: { label: "login rejected", tone: "reject" },
  not_configured: { label: "not configured", tone: null },
};

function providerLine(s: SubtitleSettings): string {
  switch (s.provider_state) {
    case "ok": {
      const parts = ["Logged in", `VIP ${s.vip ? "yes" : "no"}`];
      if (s.quota_remaining >= 0) parts.push(s.allowed_downloads > 0 ? `${s.quota_remaining}/${s.allowed_downloads} downloads left today` : `${s.quota_remaining} downloads left today`);
      return parts.join(" · ");
    }
    case "quota_spent":
      return s.quota_reset_at > 0 ? `Daily quota used — resumes ${new Date(s.quota_reset_at * 1000).toLocaleString()}.` : s.provider_detail;
    default:
      return s.provider_detail;
  }
}

// OpenSubtitlesCard is the download source's real state and, for admins, its credentials
// right here (the same saved keys as Settings → Connections; a value is never shown back).
// Saving tests the login straight away; Test signs in fresh.
function OpenSubtitlesCard({ settings, onChanged, children }: { settings: SubtitleSettings; onChanged: () => void; children?: ReactNode }) {
  const { user } = useMe();
  const admin = isAdmin(user);
  const keys = useAPIKeys(admin, onChanged);
  const chip = PROVIDER_CHIP[settings.provider_state] ?? PROVIDER_CHIP.not_configured;
  const tone = chip.tone ? `var(--${chip.tone})` : "var(--ink-faint)";
  return (
    <div className={card} style={cardStyle}>
      <div className="text-md font-bold">OpenSubtitles (optional download source)</div>
      <div className="mt-0.5 text-sm text-ink-faint">A download source Arrmada tries before AI. Optional — embedded extraction and local AI work without it. Downloading needs a free account as well as the API key.</div>
      <div className="mt-3 flex items-center gap-2 text-sm">
        <span className="rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: chip.tone ? `var(--${chip.tone}-soft)` : "var(--panel-2)", color: tone }}>{chip.label}</span>
        <span className="text-ink-dim" style={settings.provider_state === "auth_failed" ? { color: "var(--reject-text)" } : undefined}>{providerLine(settings)}</span>
      </div>
      {admin
        ? <KeyEditor ids={keyRowsFor("opensubtitles_api")} ctl={keys} />
        : <p className="m-0 mt-2 text-sm text-ink-faint">An admin enters the OpenSubtitles API key and account under <Link to={LINKS.apiKeys} style={{ color: "var(--accent-text)" }}>Settings → Connections</Link>.</p>}
      {children}
    </div>
  );
}

function LocalAI({ backend, note, cpuOnly, children }: { backend?: string; note?: string; cpuOnly?: boolean; children?: ReactNode }) {
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
        <div className="text-md font-bold">Local AI (whisper)</div>
        <span className="rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: st?.ready ? "var(--good-soft, rgba(127,176,105,.16))" : "var(--panel-2)", color: st?.ready ? "var(--good-text)" : "var(--ink-faint)" }}>
          {st?.ready ? "ready" : st?.binary_ready ? "needs a model" : "not installed"}
        </span>
      </div>
      <div className="mt-0.5 text-sm text-ink-faint">Generates subtitles from a file's audio when no better source exists — 100% local, no key. Download a model to enable it (large files, runs in the background).</div>
      {/* Which compute backend the last run used. A Vulkan build with no visible device runs
          on the CPU without complaint, so "ready" alone can hide a GPU that isn't being used. */}
      {st?.ready && (
        <div className="mt-1.5 text-sm" style={{ color: backend === "vulkan" || backend === "sycl" ? "var(--good-text)" : "var(--ink-dim)" }}>
          {backend === "sycl"
            ? "Running on the GPU (Intel oneAPI)."
            : backend === "vulkan"
            ? "Running on the GPU (Vulkan)."
            : backend === "cpu" && cpuOnly
              ? "Running on the CPU: the ARM build has no GPU acceleration for AI subtitles, so each file takes a while: on a small board like a Raspberry Pi, possibly longer than the film itself."
            : backend === "cpu"
              ? "Running on the CPU. If this host has a GPU, check that /dev/dri is passed to the container and vulkaninfo inside it lists a device."
              : "Backend not known yet — it's reported after the first subtitle is generated."}
        </div>
      )}
      {/* The Intel oneAPI build is tried first; when it's set aside, say why rather than
          quietly running slower. */}
      {st?.ready && note && (
        <div className="mt-1 text-sm" style={{ color: "var(--avoid-text)" }}>Intel oneAPI build not in use — {note}</div>
      )}
      {st && !st.binary_ready && <div className="mt-2 text-sm" style={{ color: "var(--avoid-text)" }}>whisper-cli isn't in this build yet — update to a build that bundles it.</div>}
      <div className="mt-3 flex flex-col gap-2">
        {(st?.models ?? []).map((m) => (
          <div key={m.name} className="flex items-center justify-between gap-3 rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }}>
            <div className="min-w-0">
              <div className="text-sm font-semibold">{m.label}</div>
              <div className="font-mono text-xs text-ink-faint">{m.name} · ~{m.size_mb >= 1000 ? `${(m.size_mb / 1000).toFixed(1)} GB` : `${m.size_mb} MB`}</div>
            </div>
            {m.present ? (
              <span className="rounded px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--good-soft, rgba(127,176,105,.16))", color: "var(--good-text)" }}>installed ✓</span>
            ) : m.downloading ? (
              <span className="rounded px-2 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>downloading…</span>
            ) : (
              <div className="flex flex-none items-center gap-2">
                {/* What this download would set going: files parked for want of a model. */}
                {!!st?.waiting && waitingFor(st) === m.name && (
                  <span className="text-xs" style={{ color: "var(--avoid-text)" }}>{st.waiting} file{st.waiting === 1 ? " is" : "s are"} waiting for {m.name.includes("turbo") ? "a model" : "large-v3"}</span>
                )}
                <button onClick={() => download(m.name)} disabled={!st?.binary_ready} className="rounded-lg px-3 py-1.5 text-xs font-semibold disabled:opacity-40" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>Download</button>
              </div>
            )}
          </div>
        ))}
      </div>
      <div className="mt-2 text-xs text-ink-faint"><b>turbo</b> transcribes speech in any language into a subtitle in that same language; it can't translate. <b>large-v3</b> is needed to translate foreign audio into English. An audio track with no language tag is listened to first, so turbo covers it when it turns out to be the language you want.</div>
      {children}
    </div>
  );
}

// waitingFor is the model the parked files wait on: large-v3 once any model is installed
// (only translating needs more), otherwise the first model offered.
function waitingFor(st: WhisperStatus): string | undefined {
  const missing = st.models.filter((m) => !m.present);
  if (missing.length === 0) return undefined;
  if (missing.length < st.models.length) return missing.find((m) => !m.name.includes("turbo"))?.name ?? missing[0].name;
  return missing[0].name;
}

function ToggleRow({ label, save }: { label: string; save: AutoSave<boolean> }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className="text-base font-semibold">{label}</label>
        <Switch id={id} checked={save.value} onChange={(v) => void save.commit(v)} />
      </div>
      {save.status !== "idle" && <div className="flex justify-end"><SavedTick save={save} /></div>}
    </div>
  );
}
