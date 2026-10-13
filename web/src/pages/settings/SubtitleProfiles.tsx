import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { LanguagePicker } from "../../components/LanguagePicker";
import { langName, useLanguages, type Language } from "../../lib/languages";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { Button, IconButton, useConfirm, useToast } from "../../ui";
import { card, cardStyle } from "../subtitles/shared";
import {
  plainItem, profileApi, profilesChanged, useProfiles,
  type ProfileItem, type ProfileSDH, type ProfileVariant, type SubtitleOverride, type SubtitleProfile,
} from "../subtitles/profileApi";

// Language profiles: per language, whether the full subtitle, the forced one (only the
// foreign-dialogue lines) or both are wanted; SDH taste; and "only when the audio is in
// another language". The Default profile is the kept-language list above — its languages
// are edited there, their options here. Other profiles are for the titles pinned to them
// from the Subtitles Library (anime, a kids' show…).

const VARIANTS: { value: ProfileVariant; label: string }[] = [
  { value: "full", label: "Full subtitle" },
  { value: "forced", label: "Forced only" },
  { value: "both", label: "Full + forced" },
];
const SDH: { value: ProfileSDH; label: string }[] = [
  { value: "any", label: "SDH as a fallback" },
  { value: "prefer", label: "Prefer SDH" },
  { value: "avoid", label: "Never SDH" },
];

const field = "rounded-md px-2 py-1 text-sm disabled:opacity-40";
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

// version changes whenever the kept languages are saved above, so the Default profile's
// rows follow at once.
export function SubtitleProfiles({ version }: { version: string }) {
  const { data, error, refetch } = useProfiles();
  const languages = useLanguages();
  // Refetch when the kept languages change above (not on mount: the query loads itself).
  const seen = useRef(version);
  useEffect(() => {
    if (seen.current === version) return;
    seen.current = version;
    void refetch();
  }, [version, refetch]);
  const profiles = data?.profiles ?? [];
  const def = profiles.find((p) => p.is_default);
  const others = profiles.filter((p) => !p.is_default);
  return (
    <div className={card} style={cardStyle}>
      <div className="text-md font-bold">Language profiles</div>
      <div className="mt-0.5 text-sm text-ink-faint">
        Per language: the full subtitle, the <b>forced</b> one (just the lines in another language — signs, a scene in Klingon), or both;
        how to treat <b>SDH</b> (sound cues for the hard of hearing); and whether to skip it when the audio is already in that language.
        Every title uses Default unless you pin it to another profile from the <Link to="/subtitles?tab=library" style={{ color: "var(--accent)" }}>Subtitles Library</Link>.
      </div>
      {!data ? (
        <p className="m-0 mt-3 text-sm text-ink-dim">{error ? error.message : "Loading…"}</p>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          {def && <ProfileEditor profile={def} languages={languages} overrides={[]} />}
          {others.map((p) => (
            <ProfileEditor key={p.id} profile={p} languages={languages} overrides={data.overrides.filter((o) => o.profile_id === p.id)} />
          ))}
          <NewProfile from={def} />
        </div>
      )}
    </div>
  );
}

function ProfileEditor({ profile, languages, overrides }: { profile: SubtitleProfile; languages: Language[] | null; overrides: SubtitleOverride[] }) {
  const toast = useToast();
  const confirm = useConfirm();
  const [name, setName] = useState(profile.name);
  useEffect(() => setName(profile.name), [profile.name]);
  // A name typed but not yet saved (it saves when the field loses focus).
  const markClean = useUnsavedGuard(!profile.is_default && name.trim() !== profile.name, "Discard the profile's new name?");
  const run = async (fn: () => Promise<unknown>, done?: string) => {
    try {
      await fn();
      profilesChanged();
      if (done) toast(done, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
      profilesChanged(); // show what's really saved
    }
  };
  const saveItems = (items: ProfileItem[]) => run(() => profileApi.update(profile.id, { items }));
  const setLangs = (codes: string[]) => {
    if (codes.length === 0) { toast("A profile needs at least one language.", { tone: "error" }); return; }
    void saveItems(codes.map((c) => profile.items.find((it) => it.lang === c) ?? plainItem(c)));
  };
  const rename = () => {
    markClean(); // saving now: leaving the page mustn't ask about it
    const n = name.trim();
    if (!n || n === profile.name) { setName(profile.name); return; }
    void run(() => profileApi.update(profile.id, { name: n }), `Renamed to ${n}`);
  };
  const remove = async () => {
    const ok = await confirm({
      title: `Delete the ${profile.name} profile?`,
      body: profile.titles ? `The ${profile.titles} title${profile.titles === 1 ? "" : "s"} pinned to it go back to Default.` : undefined,
      confirmLabel: "Delete",
      tone: "danger",
    });
    if (ok) void run(() => profileApi.remove(profile.id), `Deleted ${profile.name}`);
  };
  return (
    <div className="rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        {profile.is_default ? (
          <div className="text-base font-bold">Default <span className="font-normal text-ink-faint">— the kept languages above, for every title not pinned elsewhere</span></div>
        ) : (
          <input
            value={name}
            aria-label="Profile name"
            maxLength={60}
            onChange={(e) => setName(e.target.value)}
            onBlur={rename}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
            className="min-w-0 rounded-md px-2 py-1 text-base font-bold"
            style={fieldStyle}
          />
        )}
        {!profile.is_default && <Button size="sm" variant="ghost" onClick={() => void remove()}>Delete</Button>}
      </div>
      {!profile.is_default && (
        <div className="mt-2">
          <LanguagePicker value={profile.items.map((it) => it.lang)} onChange={setLangs} languages={languages} label={`Add a language to ${profile.name}`} />
        </div>
      )}
      <div className="mt-2 flex flex-col gap-1.5">
        {profile.items.map((it, i) => (
          <ItemRow
            key={it.lang}
            item={it}
            name={langName(it.lang, languages)}
            onChange={(next) => void saveItems(profile.items.map((x, j) => (j === i ? next : x)))}
          />
        ))}
      </div>
      {overrides.length > 0 && (
        <div className="mt-2.5 flex flex-wrap items-center gap-1.5 text-xs text-ink-faint">
          <span>Used by</span>
          {overrides.map((o) => (
            <span key={`${o.kind}:${o.id}`} className="inline-flex items-center gap-1 rounded-full px-2 py-0.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
              {o.title || `${o.kind} #${o.id}`}
              <IconButton label={`Put ${o.title || "this title"} back on Default`} className="h-4 w-4 text-xs" onClick={() => void run(() => profileApi.setOverride(o.kind, o.id, 0))}>✕</IconButton>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ItemRow({ item, name, onChange }: { item: ProfileItem; name: string; onChange: (next: ProfileItem) => void }) {
  const forcedOnly = item.variant === "forced";
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg px-2.5 py-1.5" style={{ background: "var(--panel-2)" }}>
      <span className="min-w-[110px] flex-1 text-sm font-semibold">{name}</span>
      <select aria-label={`${name}: which subtitle`} value={item.variant} onChange={(e) => onChange({ ...item, variant: e.target.value as ProfileVariant })} className={field} style={fieldStyle}>
        {VARIANTS.map((v) => <option key={v.value} value={v.value}>{v.label}</option>)}
      </select>
      <select aria-label={`${name}: SDH`} value={item.sdh} disabled={forcedOnly} onChange={(e) => onChange({ ...item, sdh: e.target.value as ProfileSDH })} className={field} style={fieldStyle}>
        {SDH.map((v) => <option key={v.value} value={v.value}>{v.label}</option>)}
      </select>
      <label className="flex items-center gap-1.5 text-sm text-ink-dim" title="The full subtitle is skipped for a file whose main audio is already this language. Forced subtitles are kept either way — they're for exactly those films.">
        <input type="checkbox" checked={item.only_if_audio_differs} disabled={forcedOnly} onChange={(e) => onChange({ ...item, only_if_audio_differs: e.target.checked })} />
        Only when the audio is another language
      </label>
    </div>
  );
}

function NewProfile({ from }: { from?: SubtitleProfile }) {
  const toast = useToast();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const markClean = useUnsavedGuard(name.trim() !== "", "Discard the new profile you were naming?");
  const create = async () => {
    const n = name.trim();
    if (!n) return;
    markClean();
    setBusy(true);
    try {
      // A new profile starts as a copy of Default; change it from there.
      await profileApi.create(n, from?.items ?? [plainItem("en")]);
      setName("");
      profilesChanged();
      toast(`Added ${n} — pin titles to it from the Subtitles Library`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => { e.preventDefault(); void create(); }}>
      <input value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="New profile name (e.g. Anime)" aria-label="New profile name" className="w-[240px] rounded-lg px-3 py-1.5 text-base" style={fieldStyle} />
      <Button type="submit" size="sm" busy={busy} disabled={!name.trim()}>Add profile</Button>
    </form>
  );
}
