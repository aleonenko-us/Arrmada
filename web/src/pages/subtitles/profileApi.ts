import { req } from "../../lib/api";
import { invalidate, useQuery } from "../../lib/query";

// Subtitle language profiles (internal/subtitles/profiles.go): which subtitles each title
// wants, by language and variant, and the titles pinned to a profile other than Default.
// Staff-only, so the calls live here in the staff chunks rather than in lib/api.ts.

export type ProfileVariant = "full" | "forced" | "both";
export type ProfileSDH = "any" | "prefer" | "avoid";

export interface ProfileItem {
  lang: string;
  variant: ProfileVariant;
  sdh: ProfileSDH;
  only_if_audio_differs: boolean;
}
export interface SubtitleProfile { id: number; name: string; items: ProfileItem[]; is_default: boolean; titles: number }
export interface SubtitleOverride { kind: "movie" | "series"; id: number; profile_id: number; title?: string }
export interface ProfileList { profiles: SubtitleProfile[]; overrides: SubtitleOverride[] }

const KEY = "subtitles:profiles";

export const profileApi = {
  list: () => req<ProfileList>("/api/v1/subtitles/profiles"),
  create: (name: string, items: ProfileItem[]) =>
    req<SubtitleProfile>("/api/v1/subtitles/profiles", { method: "POST", body: JSON.stringify({ name, items }) }),
  update: (id: number, body: { name?: string; items?: ProfileItem[] }) =>
    req<SubtitleProfile>(`/api/v1/subtitles/profiles/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  remove: (id: number) => req<void>(`/api/v1/subtitles/profiles/${id}`, { method: "DELETE" }),
  // profileId 0 (or the default's id) puts the title back on Default.
  setOverride: (kind: "movie" | "series", id: number, profileId: number) =>
    profileId
      ? req<void>(`/api/v1/subtitles/overrides/${kind}/${id}`, { method: "PUT", body: JSON.stringify({ profile_id: profileId }) })
      : req<void>(`/api/v1/subtitles/overrides/${kind}/${id}`, { method: "DELETE" }),
};

// useProfiles is the shared, cached profile list; every Library row's picker reads one answer.
export function useProfiles() {
  return useQuery(KEY, profileApi.list, { staleMs: 60_000 });
}

// profilesChanged drops the cached list after a write.
export function profilesChanged() {
  invalidate(KEY);
}

export const plainItem = (lang: string): ProfileItem => ({ lang, variant: "full", sdh: "any", only_if_audio_differs: false });
