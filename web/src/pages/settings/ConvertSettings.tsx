import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { SavedTick } from "../../components/settings/auto";
import { useScrollWhenLoaded } from "../../components/settings/ui";
import { api, type ConvertSettings as Settings } from "../../lib/api";
import { fmtBytes } from "../../lib/disposal";
import { LINKS } from "../../lib/links";
import { useAutoSave, type AutoSave } from "../../lib/useAutoSave";
import { SwitchOnDialog } from "../convert/ConfirmConvert";

// Settings → Subtitles & Convert → Convert: when Convert runs, what it converts to, which
// tracks it keeps, and where it works. It used to be the Convert page's Settings tab; that
// page now links here (Configure →) and reads these through its own GET. Each control saves
// itself — switches and choices at once, text when you leave the field — except the
// transcode folder, which is a path and saves only when you confirm it.
export const CONVERT_SETTINGS_ID = "convert";

const inp = "rounded-lg px-2.5 py-1.5 text-[12px]";
const inpStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;
const errStyle = { ...inpStyle, border: "1px solid var(--reject)" } as const;
const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;
const accentBtn = { background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" } as const;

export function ConvertSettings() {
  const [s, setS] = useState<Settings | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.convertSettings().then(setS).catch((e: Error) => setError(e.message)); }, []);
  useScrollWhenLoaded(CONVERT_SETTINGS_ID, !!s);
  // Each save sends just its own keys and takes back just those, so two saves answering out
  // of order can't put an older value back.
  const commit = useCallback(async (p: Partial<Settings>) => {
    const next = await api.updateConvertSettings(p);
    setS((cur) => {
      if (!cur) return next;
      const merged = { ...cur } as Record<string, unknown>;
      for (const k of Object.keys(p)) merged[k] = (next as unknown as Record<string, unknown>)[k];
      return merged as unknown as Settings;
    });
  }, []);
  return (
    <div id={CONVERT_SETTINGS_ID} className="flex scroll-mt-20 flex-col gap-4">
      <h2 className="m-0 text-[15px] font-bold">Convert</h2>
      {s ? <ConvertForm s={s} commit={commit} /> : <p className="m-0 text-[12.5px] text-ink-dim">{error ?? "Loading…"}</p>}
    </div>
  );
}

function minutesApart(a: string | undefined, b: string): number {
  const mins = (t?: string) => { const m = /^(\d{1,2}):(\d{2})$/.exec(t ?? ""); return m ? +m[1] * 60 + +m[2] : null; };
  const x = mins(a), y = mins(b);
  if (x === null || y === null) return 0;
  const d = Math.abs(x - y);
  return Math.min(d, 1440 - d);
}

type Commit = (p: Partial<Settings>) => Promise<void>;
type Hours = { hours_start: string; hours_end: string };

function ConvertForm({ s, commit }: { s: Settings; commit: Commit }) {
  // One hook per control: each shows its own Saving… / Saved ✓ / error.
  const put = <K extends keyof Settings>(k: K) => (v: Settings[K]) => commit({ [k]: v } as Partial<Settings>);
  const auto = useAutoSave(s.auto, put("auto"));
  // Switching on asks first, with what it will do across the library; off saves at once.
  const [askSwitchOn, setAskSwitchOn] = useState(false);
  const autoSwitch: AutoSave<boolean> = {
    ...auto,
    commit: async (on) => {
      if (on === true && !auto.value) { setAskSwitchOn(true); return false; }
      return auto.commit(on);
    },
  };
  const hours = useAutoSave<Hours>({ hours_start: s.hours_start, hours_end: s.hours_end }, (v) => commit(v), {
    equal: (a, b) => a.hours_start === b.hours_start && a.hours_end === b.hours_end,
    validate: (v) => ((v.hours_start === "") !== (v.hours_end === "") ? "Set both a start and an end time." : null),
  });
  const pause = useAutoSave(s.pause_watching, put("pause_watching"));
  const av1 = useAutoSave(s.allow_av1, put("allow_av1"));
  const gpu = useAutoSave(s.use_gpu, put("use_gpu"));
  const crop = useAutoSave(s.crop, put("crop"));
  const audioLangs = useAutoSave(s.keep_audio_langs, put("keep_audio_langs"));
  const origLang = useAutoSave(s.keep_original_lang, put("keep_original_lang"));
  const commentary = useAutoSave(s.drop_commentary, put("drop_commentary"));
  const subLangs = useAutoSave(s.keep_sub_langs, put("keep_sub_langs"));
  const imageSubs = useAutoSave(s.image_subs, put("image_subs"), { keepOnError: false });
  const tidy = useAutoSave(s.tidy_tracks, put("tidy_tracks"));
  const scratch = useAutoSave(s.scratch_dir, put("scratch_dir"), { equal: (a, b) => a.trim() === b.trim() });
  const vaapi = useAutoSave(s.vaapi_device || "", put("vaapi_device"), { keepOnError: false });
  const cores = useAutoSave(String(s.cpu_cores), (v) => commit({ cpu_cores: Number(v) }), {
    validate: (v) => (/^\d{1,3}$/.test(v.trim()) && Number(v) <= 256 ? null : "CPU cores needs a number from 0 to 256."),
  });
  const workers = useAutoSave(String(s.workers), (v) => commit({ workers: Number(v) }), {
    validate: (v) => (/^[1-4]$/.test(v.trim()) ? null : "Conversions at once must be 1–4."),
  });

  const [hw, setHw] = useState<{ dir: string; free: number; need: number; needTitle: string; devices: { path: string; pci: string; vendor: string }[] } | null>(null);
  const loadHw = useCallback(() => {
    api.convertHardware().then((h) => setHw({ dir: h.scratch_dir, free: h.scratch_free_bytes, need: h.scratch_need_bytes ?? 0, needTitle: h.scratch_need_title ?? "", devices: h.render_devices ?? [] })).catch(() => {});
  }, []);
  useEffect(loadHw, [loadHw]);
  // A new transcode folder changes the free space shown under it.
  useEffect(() => { if (scratch.status === "saved") loadHw(); }, [scratch.status, loadHw]);

  const scheduled = !!(hours.value.hours_start || hours.value.hours_end);
  // The switch is the hours pair seen as on/off: on starts with 01:00–07:00, off clears both.
  const scheduleSwitch: AutoSave<boolean> = {
    ...hours,
    value: scheduled,
    set: () => {},
    commit: (on) => hours.commit(on ? { hours_start: "01:00", hours_end: "07:00" } : { hours_start: "", hours_end: "" }),
  };
  const browserTime = new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  const clockSkewed = minutesApart(s.server_time, browserTime) > 5;
  const textKeys = (save: AutoSave<string>) => (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") { e.preventDefault(); void save.commit(); }
    if (e.key === "Escape" && save.dirty) { e.preventDefault(); save.revert(); }
  };
  const textProps = (save: AutoSave<string>) => ({
    value: save.value,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => save.set(e.target.value),
    onBlur: () => void save.commit(),
    onKeyDown: textKeys(save),
    "aria-invalid": save.status === "error" || undefined,
    style: save.status === "error" ? errStyle : inpStyle,
  });

  return (
    <div className="flex flex-col gap-4">
      <Section title="Automatic conversion">
        {askSwitchOn && (
          <SwitchOnDialog onCancel={() => setAskSwitchOn(false)} switchOn={async () => {
            if (!(await auto.commit(true))) throw new Error("Couldn't switch it on — try again in a moment.");
            setAskSwitchOn(false);
          }} />
        )}
        <Toggle save={autoSwitch} label="Convert my library automatically"
          hint="Works through everything that's worth converting, biggest savings first, one file at a time. Files you convert from the Library start within seconds either way, after any other file you asked for — an automatic conversion in progress waits for them." />
        <Toggle
          save={scheduleSwitch}
          label="Only during set hours" hint={`A conversion still running when the hours end is paused and picks up again next time${s.can_pause ? "" : " (pausing needs the Linux container — elsewhere it finishes first)"}.`} />
        {scheduled && (
          <div className="ml-6 flex flex-col gap-1.5">
            <span className="flex flex-wrap items-center gap-1.5 text-[12px]">
              <input type="time" aria-label="Start" value={hours.value.hours_start} onChange={(e) => hours.set({ ...hours.value, hours_start: e.target.value })} onBlur={() => void hours.commit()} className={inp} style={hours.status === "error" ? errStyle : inpStyle} />
              <span className="text-ink-faint">to</span>
              <input type="time" aria-label="End" value={hours.value.hours_end} onChange={(e) => hours.set({ ...hours.value, hours_end: e.target.value })} onBlur={() => void hours.commit()} className={inp} style={hours.status === "error" ? errStyle : inpStyle} />
            </span>
            <span className={`text-[11px] ${clockSkewed ? "" : "text-ink-faint"}`} style={clockSkewed ? { color: "var(--avoid)" } : undefined}>
              {clockSkewed ? "⚠ " : ""}Read on the server's clock: {s.server_time} {s.server_tz}{clockSkewed ? ` — yours says ${browserTime}. Set TZ in your .env and recreate the container (docker compose up -d).` : "."}
            </span>
          </div>
        )}
        <Toggle save={pause} label="Pause while someone is watching Plex"
          hint={s.plex_watching_known ? "Nothing new starts, and a running conversion is frozen until the stream stops." : <>Needs Plex watch monitoring turned on (<Link to={LINKS.plexMonitoring} className="font-semibold" style={{ color: "var(--accent)" }}>Settings → Plex</Link>) — until then this does nothing.</>} />
      </Section>

      <Section title="Format" desc="HEVC by default: the best quality, and it plays on most devices made since about 2016. Dolby Vision files are converted from their HDR10 or HLG base — the Dolby Vision layer is removed.">
        <Toggle save={av1} label="My devices can play AV1"
          hint={<>Turn on only if every TV, phone and streaming box you watch on decodes AV1 — older Apple TVs, Shields and Fire Sticks don't, and Plex would transcode on the fly. When on, each file gets a quick side-by-side test and goes to AV1 only if it's clearly smaller at the same quality; otherwise HEVC. Files with HDR10+ always stay HEVC.</>} />
        {s.has_gpu && (
          <Toggle save={gpu} label="Use my GPU"
            hint={`Many times faster, but files come out somewhat bigger for the same quality. HDR always encodes on the CPU (the GPU can't carry its metadata).${s.gpu_does_av1 ? "" : " Your GPU has no AV1 encoder, so GPU mode converts to HEVC."}`} />
        )}
        <Toggle save={crop} label="Remove black bars"
          hint="Widescreen films are encoded without the black bars above and below (or beside) the picture. The picture and its shape stay exactly the same and your TV letterboxes it as before. Files come out slightly smaller, and the quality check judges the picture alone instead of counting the bars as a perfect match. The film is checked about every 25 seconds (40 to 400 places): only what's black in every one of those frames is removed, and if any of them shows a different shape (IMAX or open-matte scenes) the film keeps its full frame. A wider shot shorter than the gap between checks could still be trimmed, which is why this is off unless you turn it on. Files that keep image subtitles keep their bars too — those subtitles are placed on the full frame." />
        {!s.hdr10plus_tool && <p className="text-[11px]" style={{ color: "var(--avoid)" }}>hdr10plus_tool isn't installed, so HDR10 files in HEVC are left alone (their HDR10+ couldn't be checked).</p>}
      </Section>

      <Section title="Audio & subtitles" desc="Tracks you don't keep are removed while converting — or on their own, which only copies the file (no re-encode). Audio is never re-encoded: Atmos, TrueHD and DTS-HD pass through untouched.">
        <Field label="Keep audio in" hint="comma-separated, e.g. en, ja · blank = keep every language · the first one becomes the default track" save={audioLangs}>
          <input aria-label="Keep audio in" placeholder="all" className={`${inp} w-[190px]`} {...textProps(audioLangs)} />
        </Field>
        <Toggle save={origLang} label="Also keep the original language"
          hint="A Japanese film keeps its Japanese track even if you only listed English." />
        <Toggle save={commentary} label="Remove commentary tracks" hint="Tracks flagged or titled as commentary." />
        <Field label="Keep subtitles in" hint="comma-separated, e.g. en · blank = keep every language" save={subLangs}>
          <input aria-label="Keep subtitles in" placeholder="all" className={`${inp} w-[190px]`} {...textProps(subLangs)} />
        </Field>
        <div role="radiogroup" aria-label="Image subtitles">
          <div className="flex flex-wrap items-center gap-2 text-[12px] font-semibold">Image subtitles (PGS, VobSub) <SavedTick save={imageSubs} /></div>
          <div className="mb-1.5 text-[10.5px] text-ink-faint">Plex has to burn these into the picture, which transcodes the video every time they're on.</div>
          {([["keep", "Keep them"], ["when_text", "Remove them when a text version exists (embedded, or an .srt from Subtitles)"], ["remove", "Always remove them — Subtitles can fetch text ones"]] as const).map(([v, label]) => (
            <label key={v} className="flex cursor-pointer items-center gap-2 py-0.5 text-[12px]">
              <input type="radio" name="image_subs" checked={imageSubs.value === v} onChange={() => void imageSubs.commit(v)} />{label}
            </label>
          ))}
        </div>
        <Toggle save={tidy} label="Tidy tracks even when that's all a file needs"
          hint="Off: a file whose picture is already fine is only rewritten when dropping tracks frees at least 10% (a big foreign-language audio track does). Its other track changes happen when it's re-encoded, or with Fix tracks. On: every file is tidied — which rewrites it end to end, often to shed a few small subtitle tracks." />
        <p className="m-0 text-[11px] text-ink-faint">Untagged tracks are kept. A language filter that would remove every audio track or every subtitle removes none — the tags are wrong.</p>
      </Section>

      <details className="rounded-xl p-4" style={cardStyle}>
        <summary className="cursor-pointer text-[13.5px] font-bold">Advanced</summary>
        <div className="mt-3 flex flex-col gap-3.5">
          <Field label="Transcode folder" hint="Fast storage (an SSD/NVMe pool), never the array. Blank = the default. Saved when you press Enter or Use this folder." save={scratch}>
            <span className="flex flex-wrap items-center gap-2">
              <input
                type="text"
                aria-label="Transcode folder"
                value={scratch.value}
                onChange={(e) => scratch.set(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && scratch.dirty) { e.preventDefault(); void scratch.commit(scratch.value.trim()); }
                  if (e.key === "Escape" && scratch.dirty) { e.preventDefault(); scratch.revert(); }
                }}
                placeholder="/transcode"
                className={`${inp} w-[240px]`}
                style={scratch.status === "error" ? errStyle : inpStyle}
              />
              {scratch.dirty && scratch.status !== "saving" && (
                <button onClick={() => void scratch.commit(scratch.value.trim())} className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={accentBtn}>Use this folder</button>
              )}
            </span>
          </Field>
          {/* Green only when the biggest of the next files fits: one 4K remux can need ~90 GB,
              so a fixed threshold said "fine" right before a file failed for space. */}
          {hw && <div className="text-[11px] text-ink-faint">Using <span className="font-mono text-ink-dim">{hw.dir}</span> · <b style={{ color: hw.free >= hw.need ? "var(--good)" : "var(--avoid)" }}>{fmtBytes(hw.free)}</b> free{hw.need > 0 && <> · largest of the next 20 files{hw.needTitle ? <> (“{hw.needTitle}”)</> : null} needs ~{fmtBytes(hw.need)}</>}</div>}
          {hw && hw.devices.length > 1 && (
            <Field label="GPU device" hint="The discrete card is usually the higher renderD number" save={vaapi}>
              <select aria-label="GPU device" value={vaapi.value} onChange={(e) => void vaapi.commit(e.target.value)} className={`${inp} w-[280px]`} style={inpStyle}>
                <option value="">Default (renderD128)</option>
                {hw.devices.map((dev) => <option key={dev.path} value={dev.path}>{dev.path.replace("/dev/dri/", "")} · {dev.vendor}{dev.pci ? ` · ${dev.pci}` : ""}</option>)}
              </select>
            </Field>
          )}
          <Field label="CPU cores" hint="How many cores a CPU conversion may use · 0 = half the machine" save={cores}>
            <input aria-label="CPU cores" inputMode="numeric" className={`${inp} w-[80px]`} {...textProps(cores)} onChange={(e) => cores.set(e.target.value.replace(/[^0-9]/g, "").slice(0, 3))} />
          </Field>
          <Field label="Conversions at once" hint="1 is right for CPU; 2 can help with a GPU" save={workers}>
            <input aria-label="Conversions at once" inputMode="numeric" className={`${inp} w-[70px]`} {...textProps(workers)} onChange={(e) => workers.set(e.target.value.replace(/[^0-9]/g, "").slice(0, 1))} />
          </Field>
        </div>
      </details>
    </div>
  );
}

function Toggle({ save, label, hint, disabled }: { save: AutoSave<boolean>; label: string; hint: ReactNode; disabled?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5">
      <label className={`flex items-start gap-2.5 text-[12.5px] ${disabled ? "opacity-50" : "cursor-pointer"}`}>
        <input type="checkbox" checked={save.value} disabled={disabled} onChange={(e) => void save.commit(e.target.checked)} className="mt-0.5" />
        <span><b className="font-semibold">{label}</b><span className="mt-0.5 block text-[11px] leading-snug text-ink-faint">{hint}</span></span>
      </label>
      {save.status !== "idle" && <span className="ml-6"><SavedTick save={save} /></span>}
    </div>
  );
}

function Section({ title, desc, children }: { title: string; desc?: string; children: ReactNode }) {
  return (
    <div className="rounded-xl p-4" style={cardStyle}>
      <div className="text-[13.5px] font-bold">{title}</div>
      {desc && <div className="mt-0.5 text-[11.5px] text-ink-faint">{desc}</div>}
      <div className="mt-3 flex flex-col gap-3.5">{children}</div>
    </div>
  );
}

function Field({ label, hint, save, children }: { label: string; hint: string; save: AutoSave<unknown> | AutoSave<string>; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span><span className="block text-[12px] font-semibold">{label}</span><span className="block text-[10.5px] text-ink-faint">{hint}</span></span>
        {children}
      </div>
      {save.status !== "idle" && <div className="flex justify-end"><SavedTick save={save as AutoSave<unknown>} /></div>}
    </div>
  );
}
