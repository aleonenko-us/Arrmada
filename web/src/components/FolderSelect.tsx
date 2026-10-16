import { useEffect } from "react";
import { foldersApi, type FolderMedia, type LibraryFolders } from "../lib/foldersApi";
import { invalidate, useQuery } from "../lib/query";

// The library's folders (Settings → Library), shared by every page that shows a folder
// picker or a folder name. A minute is fresh enough: they change only in Settings, which
// invalidates them.
export const FOLDERS_KEY = "library-folders";

export function useLibraryFolders(enabled = true) {
  return useQuery<LibraryFolders>(FOLDERS_KEY, foldersApi.list, { enabled, staleMs: 60_000 });
}

/** Pages showing folders reload them (after a folder is added, renamed or removed). */
export function foldersChanged() {
  invalidate(FOLDERS_KEY);
}

const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

// FolderSelect picks the folder a title is added to, on the add and approve dialogs. It
// renders nothing at all while the library has one folder, so those dialogs look exactly
// as they did before extra folders. With several it preselects the folder the rules would
// pick; until then (or untouched) value stays undefined and the server's rules decide,
// which is the same answer.
export function FolderSelect({ media, tmdbId, value, onChange, disabled, compact, inline }: {
  media: FolderMedia;
  tmdbId?: number;
  value: number | undefined;
  onChange: (id: number | undefined) => void;
  disabled?: boolean;
  /** The add dialogs' smaller controls (the approve sheet's are taller). */
  compact?: boolean;
  /** A one-line picker for a dialog that adds from a list of results (no title yet):
   *  it offers "By the rules" first, which leaves the choice to the server per title. */
  inline?: boolean;
}) {
  const { data } = useLibraryFolders();
  const set = data?.[media];
  const multi = !!set?.multi;
  useEffect(() => {
    if (!multi || value !== undefined || !tmdbId) return;
    let alive = true;
    foldersApi.suggest(media, tmdbId)
      .then((id) => { if (alive) onChange(id); })
      .catch(() => { /* the default stays shown; the server's rules still decide */ });
    return () => { alive = false; };
    // Only a new title or a newly-multi library asks again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [multi, media, tmdbId]);

  if (!set || !multi) return null;
  if (inline) {
    return (
      <label className="flex items-center gap-2">
        <span className="font-mono text-2xs uppercase text-ink-faint">Folder</span>
        <select
          value={value === undefined ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
          disabled={disabled}
          className="rounded-lg px-2 py-1.5 text-sm"
          style={field}
        >
          <option value="">By the folder rules</option>
          {set.folders.map((f) => <option key={f.id} value={f.id}>{f.label}{f.default ? " (default)" : ""}</option>)}
        </select>
      </label>
    );
  }
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-mono text-2xs uppercase text-ink-faint">Folder</span>
      <select
        value={value ?? 0}
        onChange={(e) => onChange(Number(e.target.value))}
        disabled={disabled}
        className={compact ? "rounded-lg px-2.5 py-1.5 text-base" : "min-h-[40px] rounded-lg px-3 text-base"}
        style={field}
      >
        {set.folders.map((f) => <option key={f.id} value={f.id}>{f.label}{f.default ? " (default)" : ""}</option>)}
      </select>
    </label>
  );
}
