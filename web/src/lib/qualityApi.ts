import { req, type FitCounts, type FitItem, type FormatInfo, type MusicPreset, type ProfileImpact, type ProfileMoveCounts, type QualityCustomFormat, type QualityExplain, type QualityPreview, type QualityProfileInfo, type ReleaseList, type SeriesFitSummary, type StoredProfile } from "./api";

// One value a custom-format condition can take (quality.VocabValue): what's stored, and
// what the editor shows.
export interface VocabValue { value: string; label: string }

// A problem with one custom format (quality.FormatProblem). index is the format's position
// in the profile; condition the row's, -1 for the format as a whole.
export interface FormatProblem { format: string; index: number; condition: number; message: string }

// The custom-format editor's live check (quality.FormatReport): errors block a save,
// warnings never match, matches[i] is how many of `samples` releases format i matches.
export interface FormatReport { errors: FormatProblem[]; warnings: FormatProblem[]; matches: number[]; samples: number }

// One TRaSH format converted for a profile (quality.TrashFormat), and what didn't carry over.
export interface TrashFormat { format: QualityCustomFormat; trash_id?: string; suggested_score?: number; warnings: string[] }
// An import's answer: formats to add, and formats (or input) that couldn't be imported.
export interface TrashImport { formats: TrashFormat[]; warnings: string[] }

// Quality profile and library-fit calls. They live here rather than in api.ts so they only
// download with the staff pages that use them, not with a requester's first load.
export const qualityApi = {
  qualityProfiles: (media: string) =>
    req<{ profiles: QualityProfileInfo[]; formats: FormatInfo[]; vocabulary?: Record<string, VocabValue[]>; music_ladder?: string[]; music_presets?: MusicPreset[] }>(`/api/v1/quality/profiles?media=${media}`),
  checkFormats: (formats: QualityCustomFormat[]) =>
    req<FormatReport>("/api/v1/quality/formats/check", { method: "POST", body: JSON.stringify({ custom_formats: formats }) }),
  // Converts pasted TRaSH custom-format JSON; saves nothing.
  importTrash: (json: string, media: string) =>
    req<TrashImport>("/api/v1/quality/import-trash", { method: "POST", body: JSON.stringify({ json, media }) }),
  libraryFitProfiles: (media: string) => req<{ profiles: Record<string, FitCounts> }>(`/api/v1/library/fit/profiles?media=${media}`),
  libraryFitPreview: (profile: StoredProfile) =>
    req<{ scope: "profile" | "library"; counts: FitCounts }>("/api/v1/library/fit/preview", { method: "POST", body: JSON.stringify({ profile }) }),
  qualityTest: (body: { profile: StoredProfile; movie_id?: number; series_id?: number; season?: number }) =>
    req<ReleaseList>("/api/v1/quality/test", { method: "POST", body: JSON.stringify(body) }),
  // The builder's "Test a release name": one name judged by the profile as edited.
  qualityExplain: (body: { profile: StoredProfile; name: string; size_gb?: number; runtime_min?: number }) =>
    req<QualityExplain>("/api/v1/quality/explain", { method: "POST", body: JSON.stringify(body) }),
  setDefaultProfile: (media: string, profile: string) =>
    req<{ media: string; profile: string }>("/api/v1/quality/default", { method: "POST", body: JSON.stringify({ media, profile }) }),
  libraryFitMovies: () => req<{ items: FitItem[] }>("/api/v1/library/fit?media=movies"),
  libraryFitSeries: () => req<{ items: SeriesFitSummary[] }>("/api/v1/library/fit?media=series"),
  libraryFitEpisodes: (seriesID: number) => req<{ items: FitItem[] }>(`/api/v1/library/fit?media=series&series=${seriesID}`),
  qualityProfile: (ref: string) => req<StoredProfile>(`/api/v1/quality/profiles/${encodeURIComponent(ref)}`),
  createQualityProfile: (sp: StoredProfile) =>
    req<StoredProfile>("/api/v1/quality/profiles", { method: "POST", body: JSON.stringify(sp) }),
  updateQualityProfile: (id: number, sp: StoredProfile) =>
    req<{ status: string }>(`/api/v1/quality/profiles/${id}`, { method: "PUT", body: JSON.stringify(sp) }),
  // Deletes a profile and moves everything on it to moveTo ("" = the media's default).
  deleteQualityProfile: (id: number, moveTo: string) =>
    req<{ moved: ProfileMoveCounts; moved_to: string }>(
      `/api/v1/quality/profiles/${id}?move_to=${encodeURIComponent(moveTo)}`, { method: "DELETE" }),
  qualityPreviewSpec: (sp: StoredProfile) =>
    req<QualityPreview>("/api/v1/quality/preview", { method: "POST", body: JSON.stringify(sp) }),
  // The dry run before saving an edit to a saved profile: which files it would make eligible
  // for replacement.
  qualityImpact: (profile: StoredProfile) =>
    req<ProfileImpact>("/api/v1/quality/impact", { method: "POST", body: JSON.stringify({ profile }) }),
  // "Keep existing files": hold a profile's files out of upgrades — with onlyAffected, just the
  // ones the edited profile would make eligible for replacement (call it before saving).
  holdExistingFiles: (id: number, edited?: StoredProfile) =>
    req<{ movies: number; versions: number; episodes: number; held: number }>(
      `/api/v1/quality/profiles/${id}/hold-existing`, { method: "POST", body: JSON.stringify(edited ? { only_affected: true, profile: edited } : {}) }),
  resumeMovieUpgrades: (id: number) =>
    req<{ resumed: number }>(`/api/v1/movies/${id}/resume-upgrades`, { method: "POST" }),
  resumeSeriesUpgrades: (id: number, season?: number) =>
    req<{ resumed: number }>(`/api/v1/series/${id}/resume-upgrades${season != null ? `?season=${season}` : ""}`, { method: "POST" }),
};
