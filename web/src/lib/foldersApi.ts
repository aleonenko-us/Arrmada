import { req, type JobRef, type MediaRequest } from "./api";

// More than one folder for Movies and TV (Settings → Library). Staff read the folders —
// the add, approve and detail pages show a picker only when a library has two or more —
// and only admins change them. These calls live apart from api.ts so they download with
// the staff pages that use them, never with a requester's first load.

export type FolderMedia = "movie" | "series";
export type FolderRule = "" | "animation" | "anime" | "kids";

export interface LibraryFolder {
  /** 0 is the default folder: the one chosen above, in Media folders. */
  id: number;
  media: FolderMedia;
  path: string;
  label: string;
  rule: FolderRule;
  position: number;
  default: boolean;
  /** Titles that belong to it. */
  titles: number;
}

export interface LibraryFolderSet {
  /** More than one folder: the only time pickers, folder names and Move to folder show. */
  multi: boolean;
  folders: LibraryFolder[];
}

export interface LibraryFolders {
  movie: LibraryFolderSet;
  series: LibraryFolderSet;
}

/** A Radarr or Sonarr root folder, with the Arrmada folder it already matches (if any). */
export interface ArrRoot {
  path: string;
  titles: number;
  free_bytes: number;
  /** The Arrmada folder it is already (null: none yet). */
  folder_id: number | null;
  /** "path" (the same folder) or "name" (a folder of the same name, mounted elsewhere). */
  matched_by?: "path" | "name";
  /** Where it looks to be inside Arrmada's container, when that folder exists here. */
  suggested_path: string;
}

export interface ArrRoots {
  media: FolderMedia;
  app: string;
  roots: ArrRoot[];
}

export const FOLDER_RULES: { value: FolderRule; label: string; help: string }[] = [
  { value: "", label: "No rule — pick it by hand", help: "Nothing goes here by itself; choose it when adding or approving." },
  { value: "kids", label: "Kids", help: "Rated G, TV-Y, TV-Y7 or TV-G (and shows TMDB files under Kids)." },
  { value: "animation", label: "Animation", help: "TMDB's Animation genre." },
  { value: "anime", label: "Anime", help: "Shows with anime numbering; films that are Japanese and animated." },
];

export const foldersApi = {
  list: () => req<LibraryFolders>("/api/v1/library/folders"),
  /** The folder a title would go to by the rules, to preselect. */
  suggest: (media: FolderMedia, tmdbId: number) =>
    req<{ folder_id: number }>(`/api/v1/library/folders/suggest?media=${media}&tmdb_id=${tmdbId}`).then((r) => r.folder_id),
  add: (body: { media: FolderMedia; path: string; label?: string; rule?: FolderRule }) =>
    req<LibraryFolder>("/api/v1/system/library/folders", { method: "POST", body: JSON.stringify(body) }),
  update: (id: number, body: { label?: string; rule?: FolderRule }) =>
    req<LibraryFolder>(`/api/v1/system/library/folders/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  order: (media: FolderMedia, ids: number[]) =>
    req<LibraryFolderSet>("/api/v1/system/library/folders/order", { method: "PUT", body: JSON.stringify({ media, ids }) }),
  remove: (id: number) => req<void>(`/api/v1/system/library/folders/${id}`, { method: "DELETE" }),
  /** Moves the title's files to the folder: a job to follow. */
  move: (media: FolderMedia, id: number, folderId: number) =>
    req<JobRef & { folder?: string }>(`/api/v1/${media === "movie" ? "movies" : "series"}/${id}/move`, { method: "POST", body: JSON.stringify({ folder_id: folderId }) }),
  /** The needs-review manual pick, from the library folder the scan found it in. */
  importFolder: (media: FolderMedia, folder: string, tmdbId: number, root?: string) =>
    req<{ status: string }>(`/api/v1/${media === "movie" ? "movies" : "series"}/import`, { method: "POST", body: JSON.stringify({ folder, tmdb_id: tmdbId, root }) }),
  /** Approves a request with the folder staff chose (the approve sheet; requests go through api.approveRequest otherwise). */
  approve: (id: number, body: { quality_profile?: string; seasons?: number[]; folder_id?: number }) =>
    req<MediaRequest>(`/api/v1/requests/${id}/approve`, {
      method: "POST",
      body: JSON.stringify({ ...body, quality_profile: body.quality_profile ?? "", seasons: body.seasons?.length ? body.seasons : undefined }),
    }),
  /** Reads a Radarr or Sonarr's root folders (nothing is saved). */
  arrRoots: (app: "radarr" | "sonarr", url: string, apiKey: string) =>
    req<ArrRoots>("/api/v1/import/arr/roots", { method: "POST", body: JSON.stringify({ app, url, api_key: apiKey }) }),
};

/** The folder a title lives in, by id ("" when unknown). */
export function folderLabel(set: LibraryFolderSet | undefined, id: number | undefined): string {
  return set?.folders.find((f) => f.id === (id ?? 0))?.label ?? "";
}
