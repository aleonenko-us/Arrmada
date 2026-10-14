import { Link } from "react-router-dom";
import { LINKS } from "../../lib/links";
import { Button, EmptyState } from "../../ui";

export interface LibraryEmptyProps {
  /** "movies", "series", "books", "artists". */
  noun: string;
  /** Which library folder(s) the scan reads: "movies folder", "ebook and audiobook folders". */
  folder: string;
  /** The page's own add button label ("Add movie"), named in the text. */
  addLabel: string;
  onScan: () => void;
  scanning?: boolean;
}

// LibraryEmpty is a library page with nothing in it yet. Most installs start from files
// already on disk, so it offers the page's own Scan library and says which folder that
// reads (and where the folder is set), with the toolbar's Add as the other road. Add isn't
// repeated here: one button per action on the page.
export function LibraryEmpty({ noun, folder, addLabel, onScan, scanning }: LibraryEmptyProps) {
  return (
    <EmptyState
      title={`No ${noun} yet`}
      body={<>
        Already have {noun} on disk? <b>Scan library</b> catalogs what's in your {folder}
        (set in <Link to={LINKS.libraryFolders} className="underline" style={{ color: "var(--accent-text)" }}>Settings → Library</Link>).
        {" "}Or use <b>+ {addLabel}</b> above to search for one, and Arrmada will monitor and grab it.
      </>}
      action={<Button onClick={onScan} busy={scanning} busyLabel="Scanning…">Scan library</Button>}
    />
  );
}
