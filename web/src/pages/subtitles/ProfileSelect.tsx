import { useState } from "react";
import { useToast } from "../../ui";
import { profileApi, profilesChanged, useProfiles } from "./profileApi";

// ProfileSelect pins one movie or one show (all its episodes) to a language profile, from
// its Library row. It only appears once a profile other than Default exists, so a library
// that never uses profiles looks as it always did.
export function ProfileSelect({ kind, id, value, title, onChanged }: {
  kind: "movie" | "series";
  id: number;
  value?: number; // the pinned profile; absent = Default
  title: string;
  onChanged: () => void;
}) {
  const { data } = useProfiles();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const profiles = data?.profiles ?? [];
  if (profiles.length < 2) return null;
  const def = profiles.find((p) => p.is_default);
  const current = value ?? def?.id ?? 0;
  const change = async (next: number) => {
    setBusy(true);
    try {
      await profileApi.setOverride(kind, id, next === def?.id ? 0 : next);
      profilesChanged();
      const name = profiles.find((p) => p.id === next)?.name ?? "Default";
      toast(`${title} now uses the ${name} profile`, { tone: "good" });
      onChanged();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };
  return (
    <select
      aria-label={`Subtitle profile for ${title}`}
      title="Which language profile this title uses"
      value={current}
      disabled={busy}
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => void change(Number(e.target.value))}
      className="max-w-[140px] flex-none rounded-md px-1.5 py-1 text-[11px] disabled:opacity-50"
      style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: value ? "var(--accent)" : "var(--ink-dim)" }}
    >
      {profiles.map((p) => <option key={p.id} value={p.id}>{p.is_default ? "Default profile" : p.name}</option>)}
    </select>
  );
}
