import { useId, useState } from "react";
import { useToast } from "../../ui";
import { profileApi, profilesChanged, useProfiles } from "./profileApi";

type TitleKind = "movie" | "series";

// usePinProfile is the shared part of the two pickers: the profile list and the call that
// pins a title to one (the Default profile's id puts it back on Default).
function usePinProfile(kind: TitleKind, id: number, title: string, onChanged?: () => void) {
  const { data } = useProfiles();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const profiles = data?.profiles ?? [];
  const def = profiles.find((p) => p.is_default);
  const change = async (next: number) => {
    setBusy(true);
    try {
      await profileApi.setOverride(kind, id, next === def?.id ? 0 : next);
      profilesChanged();
      const name = profiles.find((p) => p.id === next)?.name ?? "Default";
      toast(`${title} now uses the ${name} profile`, { tone: "good" });
      onChanged?.();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };
  return { data, profiles, def, busy, change };
}

// ProfileSelect pins one movie or one show (all its episodes) to a language profile, from
// its Library row. It only appears once a profile other than Default exists, so a library
// that never uses profiles looks as it always did.
export function ProfileSelect({ kind, id, value, title, onChanged }: {
  kind: TitleKind;
  id: number;
  value?: number; // the pinned profile; absent = Default
  title: string;
  onChanged: () => void;
}) {
  const { profiles, def, busy, change } = usePinProfile(kind, id, title, onChanged);
  if (profiles.length < 2) return null;
  const current = value ?? def?.id ?? 0;
  return (
    <select
      aria-label={`Subtitle profile for ${title}`}
      title="Which language profile this title uses"
      value={current}
      disabled={busy}
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => void change(Number(e.target.value))}
      className="max-w-[140px] flex-none rounded-md px-1.5 py-1 text-xs disabled:opacity-50"
      style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: value ? "var(--accent)" : "var(--ink-dim)" }}
    >
      {profiles.map((p) => <option key={p.id} value={p.id}>{p.is_default ? "Default profile" : p.name}</option>)}
    </select>
  );
}

// TitleProfilePicker is the Movie and Series pages' "Subtitles: Default ▾", beside the
// quality profile: which language profile the title (for a show, every episode) uses.
// Like the Library's picker it only appears once there is a second profile to choose.
export function TitleProfilePicker({ kind, id, title, onChanged }: { kind: TitleKind; id: number; title: string; onChanged?: () => void }) {
  const { data, profiles, def, busy, change } = usePinProfile(kind, id, title, onChanged);
  const selectId = useId();
  if (profiles.length < 2) return null;
  const pinned = data?.overrides.find((o) => o.kind === kind && o.id === id)?.profile_id;
  const current = pinned ?? def?.id ?? 0;
  return (
    <div className="flex items-center gap-2">
      <label htmlFor={selectId} className="font-mono text-2xs uppercase text-ink-faint">Subtitles</label>
      <select
        id={selectId}
        title="Which subtitle language profile this title uses"
        value={current}
        disabled={busy}
        onChange={(e) => void change(Number(e.target.value))}
        className="rounded-lg px-2.5 py-1.5 text-sm font-medium disabled:opacity-50"
        style={{ background: pinned ? "var(--accent-soft)" : "var(--panel-2)", border: "1px solid var(--line)", color: pinned ? "var(--accent)" : "var(--ink)" }}
      >
        {profiles.map((p) => <option key={p.id} value={p.id} style={{ background: "var(--panel)", color: "var(--ink)" }}>{p.is_default ? "Default" : p.name}</option>)}
      </select>
    </div>
  );
}
