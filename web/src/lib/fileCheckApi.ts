import { req } from "./api";

// The file check (internal/filecheck): what it found inside library files — no sound,
// silence, the wrong language, a probable cam, a damaged file — and what to do about each.
// Its own module so it stays out of a requester's first download.

export type FileCheckCode = "no_audio" | "silent_audio" | "wrong_language" | "suspected_cam" | "unreadable";

export interface FileCheckFinding {
  id: number;
  kind: "movie" | "episode";
  media_id: number;
  version_id?: number;
  /** The movie, or the episode's show. */
  owner_id: number;
  season?: number;
  episode?: number;
  code: FileCheckCode;
  /** "The Matrix (1999)", "Severance S01E02". */
  title: string;
  /** "the file has no sound". */
  detail: string;
  release?: string;
  path?: string;
  first_seen: string;
  dismissed?: boolean;
  /** "" found; "replacing" blocklisted and searching; "gave_up" automatic replacement stopped. */
  state?: "" | "replacing" | "gave_up";
  /** The whole sentence, as Needs you says it. */
  text: string;
  error?: boolean;
}

export interface FileChecks {
  findings: FileCheckFinding[];
  auto_replace: boolean;
}

export const fileCheckApi = {
  list: () => req<FileChecks>("/api/v1/file-checks"),
  forMovie: (id: number) => req<FileChecks>(`/api/v1/movies/${id}/file-checks`),
  forSeries: (id: number) => req<FileChecks>(`/api/v1/series/${id}/file-checks`),
  /** Check the library now: new or changed files, or every file with all. */
  run: (all = false) => req<{ job_id?: number }>("/api/v1/file-checks/run", { method: "POST", body: JSON.stringify({ all }) }),
  /** Find a better copy: blocklist the release and search again; the file stays until one lands. */
  replace: (id: number) => req<{ message: string }>(`/api/v1/file-checks/${id}/replace`, { method: "POST" }),
  dismiss: (id: number) => req<void>(`/api/v1/file-checks/${id}/dismiss`, { method: "POST" }),
  setAutoReplace: (on: boolean) => req<{ auto_replace: boolean }>("/api/v1/file-checks/settings", { method: "PUT", body: JSON.stringify({ auto_replace: on }) }),
};

// A finding in a few words for a badge: "No sound", "Probably a cam".
export function findingBadge(code: FileCheckCode): string {
  switch (code) {
    case "no_audio": return "No sound";
    case "silent_audio": return "Silent audio";
    case "wrong_language": return "Wrong language";
    case "suspected_cam": return "Probably a cam";
    case "unreadable": return "Damaged file";
  }
  return "Looks wrong";
}
