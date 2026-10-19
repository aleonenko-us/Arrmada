import { useMemo } from "react";
import { useLibraryFolders } from "../../components/FolderSelect";
import type { FolderMedia, LibraryFolder, LibraryFolderSet } from "../../lib/foldersApi";
import { useSearchParamState } from "../../lib/useSearchState";
import type { LibraryScope } from "./LibraryGrid";

// The Folder picker on the Movies and TV library pages (GitHub #34): with several folders
// (a Kids or Cartoons folder beside the main one) the page can show one of them, as if it
// were its own library, and "+ Add" while one is shown adds into it. With one folder
// nothing shows, like every other folder control. The choice lives in the address
// (?folder=3), so a folder's view can be bookmarked or linked.

/** A title's folder: 0 (the default) when the list leaves it out. */
interface InFolder { library_folder_id?: number }

// pickedFolder is the folder ?folder= names, or undefined for all of them: when the
// library has one folder, or the id isn't one of its folders (removed since).
export function pickedFolder(set: LibraryFolderSet | undefined, raw: string): LibraryFolder | undefined {
  if (!set?.multi || raw === "") return undefined;
  return set.folders.find((f) => String(f.id) === raw);
}

export function inFolder(item: InFolder, folderId: number): boolean {
  return (item.library_folder_id ?? 0) === folderId;
}

// folderOptions is the picker's list: All folders, then each folder in its order.
export function folderOptions(set: LibraryFolderSet): { value: string; label: string }[] {
  return [{ value: "", label: "All folders" }, ...set.folders.map((f) => ({ value: String(f.id), label: f.default ? `${f.label} (default)` : f.label }))];
}

export interface FolderScope<T> {
  /** For LibraryGrid; undefined with one folder (no picker at all). */
  scope?: LibraryScope<T>;
  /** The folder shown, for the Add dialog to preselect; undefined: all (the rules decide). */
  folderId?: number;
}

export function useFolderScope<T extends InFolder>(media: FolderMedia): FolderScope<T> {
  const { data } = useLibraryFolders();
  const set = data?.[media];
  const [raw, setRaw] = useSearchParamState<string>("folder", "");
  const folder = pickedFolder(set, raw);
  const id = folder?.id;
  // Stable while the same folder shows, so the grid doesn't refilter on every render.
  const match = useMemo(() => (id === undefined ? undefined : (it: T) => inFolder(it, id)), [id]);
  if (!set?.multi) return {};
  return {
    scope: {
      picker: { label: "Folder", value: folder ? String(folder.id) : "", options: folderOptions(set), onChange: setRaw },
      match,
      name: folder?.label ?? "",
      onClear: () => setRaw(""),
    },
    folderId: id,
  };
}
