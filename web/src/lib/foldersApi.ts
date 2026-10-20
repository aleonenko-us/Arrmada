import { req, type JobRef, type MediaRequest } from "./api";

// More than one folder for Movies and TV (Settings → Library). Staff read the folders —
// the add, approve and detail pages show a picker only when a library has two or more —
// and only admins change them. These calls live apart from api.ts so they download with
// the staff pages that use them, never with a requester's first load.

export type FolderMedia = "movie" | "series";

/** A film's move, before it runs: the move never takes more than files lists. */
export interface MovePreview {
  folder: string;
  moves: { from: string; to: string; files: string[]; bytes: number }[];
  /** Why nothing would move ("" or absent when it would). */
  refused?: string;
}
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
  /** Its own downloads folder ("" = the Downloads folder): torrents for its titles save there. */
  downloads?: string;
  /** Set when that folder isn't on the library folder's filesystem (imports copy). */
  downloads_warning?: string;
  /**
   * An extra folder on disk (only from listWithDisk): "ok" with on_disk, the entries a scan
   * would look at; "empty" (a new folder, or a share that isn't mounted — never counted);
   * "missing"; "unknown" (it didn't answer in time: a dead share).
   */
  disk?: "ok" | "empty" | "missing" | "unknown";
  on_disk?: number;
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
  /** The same, with what each extra folder holds on disk (Settings → Library). */
  listWithDisk: () => req<LibraryFolders>("/api/v1/library/folders?disk=1"),
  /** Scans one Movies or TV folder (a job to follow): adding a folder doesn't import what's in it. */
  scan: (media: FolderMedia, root: string) =>
    req<{ status: string } & JobRef>(`/api/v1/${media === "movie" ? "movies" : "series"}/scan`, { method: "POST", body: JSON.stringify({ root }) }),
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
  /** Gives a folder (0: the main one) its own downloads folder, or clears it with "". */
  setDownloads: (media: FolderMedia, id: number, path: string) =>
    req<{ folder: LibraryFolder; warning?: string }>("/api/v1/system/library/folders/downloads", { method: "PUT", body: JSON.stringify({ media, id, path }) }),
  /** Moves the title's files to the folder: a job to follow. */
  move: (media: FolderMedia, id: number, folderId: number, files?: string[]) =>
    req<JobRef & { folder?: string }>(`/api/v1/${media === "movie" ? "movies" : "series"}/${id}/move`, { method: "POST", body: JSON.stringify(files ? { folder_id: folderId, files } : { folder_id: folderId }) }),
  /** What moving a film would do, moving nothing: each folder that moves whole with every file in it, or why it won't. */
  previewMove: (id: number, folderId: number) =>
    req<MovePreview>(`/api/v1/movies/${id}/move/preview`, { method: "POST", body: JSON.stringify({ folder_id: folderId }) }),
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
