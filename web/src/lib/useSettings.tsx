import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { api, type AppSettings } from "./api";
import { useMe } from "./me";
import { useAutoSave, type AutoSave, type AutoSaveOptions } from "./useAutoSave";

// One copy of /api/v1/settings shared by every Settings section. Each control saves
// itself (useSetting, below): commit PUTs just its own keys and merges just those keys of
// the answer back into the shared copy, so two sections saving at once can't overwrite
// each other's values, and nothing read-only is ever echoed back.

interface SettingsStore {
  s: AppSettings | null; // what the server last confirmed
  error: string | null;
  /** Store some keys; resolves with the server's copy once the shared one is updated. */
  commit: (p: Partial<AppSettings>) => Promise<AppSettings>;
}

const SettingsContext = createContext<SettingsStore | null>(null);

export function SettingsProvider({ children }: { children: React.ReactNode }) {
  const { setBooksEnabled, setMusicEnabled } = useMe();
  const [s, setS] = useState<AppSettings | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.settings().then(setS).catch((e: Error) => setError(e.message));
  }, []);

  const commit = useCallback(async (p: Partial<AppSettings>) => {
    const next = await api.updateSettings(p);
    // Only the keys this save sent: another key's save may have answered later than
    // this one started, and its newer value must not be replaced by this older copy.
    setS((cur) => {
      if (!cur) return next;
      const merged = { ...cur } as Record<string, unknown>;
      for (const k of Object.keys(p)) merged[k] = (next as unknown as Record<string, unknown>)[k];
      return merged as unknown as AppSettings;
    });
    // Module on/off shows in the nav and Discover straight away.
    if ("books_enabled" in p) setBooksEnabled(next.books_enabled);
    if ("music_enabled" in p) setMusicEnabled(next.music_enabled);
    return next;
  }, [setBooksEnabled, setMusicEnabled]);

  return <SettingsContext.Provider value={{ s, error, commit }}>{children}</SettingsContext.Provider>;
}

export function useSettingsStore(): SettingsStore {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error("useSettingsStore must be used inside <SettingsProvider>");
  return ctx;
}

// useLoadedSettings is useSettingsStore for a section that only renders once the settings
// have loaded (the hub shows "Loading…" until then), so s is never null inside it.
export function useLoadedSettings(): SettingsStore & { s: AppSettings } {
  const ctx = useSettingsStore();
  if (!ctx.s) throw new Error("settings used before they loaded");
  return ctx as SettingsStore & { s: AppSettings };
}

// useSetting is one key of the shared settings as an auto-saving control: pair it with
// AutoToggle / AutoText / AutoNumber / AutoSelect from components/settings/ui.
export function useSetting<K extends keyof AppSettings>(key: K, opts?: AutoSaveOptions<AppSettings[K]>): AutoSave<AppSettings[K]> {
  const { s, commit } = useLoadedSettings();
  return useAutoSave<AppSettings[K]>(s[key], (v) => commit({ [key]: v } as Partial<AppSettings>), opts);
}

// useSettingGroup is several keys that save together: the server validates them as a set
// (the disk guard's pause and resume points), so a change to either sends both.
export function useSettingGroup<K extends keyof AppSettings>(keys: readonly K[], opts?: AutoSaveOptions<Pick<AppSettings, K>>): AutoSave<Pick<AppSettings, K>> {
  const { s, commit } = useLoadedSettings();
  const saved = Object.fromEntries(keys.map((k) => [k, s[k]])) as Pick<AppSettings, K>;
  const equal = (a: Pick<AppSettings, K>, b: Pick<AppSettings, K>) => keys.every((k) => Object.is(a[k], b[k]));
  return useAutoSave<Pick<AppSettings, K>>(saved, (v) => commit(v as Partial<AppSettings>), { equal, ...opts });
}
