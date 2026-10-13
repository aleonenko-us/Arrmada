import { Link } from "react-router-dom";
import { LINKS } from "../../lib/links";
import { Button, EmptyState } from "../../ui";

export interface LibraryEmptyProps {
  /** "movies", "series", "books", "artists". */
  noun: string;
  /** Which library folder(s) the scan reads: "movies folder", "ebook and audiobook folders". */
  folder: string;
  /** The page's own add button label ("Add movie"); omitted when adding needs a key first. */
  addLabel: string;
  onAdd: () => void;
  addDisabled?: boolean;
  onScan: () => void;
  scanning?: boolean;
}

// LibraryEmpty is a library page with nothing in it yet. Most installs start from files
// already on disk, so it offers the page's own Scan library first and says which folder
// that reads (and where the folder is set), with adding one by search as the other road.
export function LibraryEmpty({ noun, folder, addLabel, onAdd, addDisabled, onScan, scanning }: LibraryEmptyProps) {
  return (
    <EmptyState
      title={`No ${noun} yet`}
      body={<>
        Already have {noun} on disk? <b>Scan library</b> catalogs what's in your {folder}
        (set in <Link to={LINKS.libraryFolders} className="underline" style={{ color: "var(--accent)" }}>Settings → Library</Link>).
        {" "}Or click <b>{addLabel}</b> to search for one, and Arrmada will monitor and grab it.
      </>}
      action={
        <div className="flex flex-wrap justify-center gap-2">
          <Button onClick={onScan} busy={scanning} busyLabel="Scanning…">Scan library</Button>
          <Button variant="primary" onClick={onAdd} disabled={addDisabled}>+ {addLabel}</Button>
        </div>
      }
    />
  );
}
