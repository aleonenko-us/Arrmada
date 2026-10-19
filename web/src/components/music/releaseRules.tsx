import type { ReleaseTypeChoices, ReleaseTypes } from "../../lib/musicApi";
import { FIELD_LABEL } from "../../ui/Field";

// The release-type and new-release rules' shared pieces (MUS-13/14): the type picker an
// artist's options and the library default use, and the words for why a refresh did or
// didn't want an album. One module, so the pages that share them share one chunk.

// foundWords says why an album a refresh found is or isn't wanted (albums.found_as): the
// server's rule is that only a new release is ever wanted on its own.
const FOUND_WORDS: Record<string, string> = {
  not_new: "Listed after a change of release types; not wanted on its own",
  back_catalogue: "Older music MusicBrainz only now lists; not wanted on its own",
  too_many: "One of many MusicBrainz listed at once; not wanted on its own",
  single_pending: "Single: checking whether its songs are already on an album",
  single_on_album: "Single already on an album; not wanted",
};

export function foundWords(foundAs: string | undefined, monitored: boolean): string {
  if (!foundAs) return "";
  if (foundAs === "new") return monitored ? "New release, wanted" : "New release; this artist's new releases are listed only";
  return FOUND_WORDS[foundAs] ?? "";
}

// ReleaseTypePicker is the two lists of MusicBrainz release-group types (MUS-14). A
// release group is listed when its kind is ticked AND every extra type it carries is
// ticked: Album + Live lists live albums, never live singles or live compilations (the
// rule Lidarr gets wrong). Used by an artist's options and the library default.

const HINTS: Record<string, string> = {
  Single: "Singles already on one of the artist's albums are listed, not wanted.",
};

function toggle(list: string[], v: string, on: boolean): string[] {
  const set = new Set(list);
  if (on) set.add(v);
  else set.delete(v);
  return [...set];
}

export function ReleaseTypePicker({
  value,
  choices,
  onChange,
  disabled,
  idPrefix,
}: {
  value: ReleaseTypes;
  choices: ReleaseTypeChoices;
  onChange: (t: ReleaseTypes) => void;
  disabled?: boolean;
  /** Keeps the checkboxes' ids unique when two pickers share a page. */
  idPrefix: string;
}) {
  const box = (kind: "primary" | "secondary", v: string) => {
    const id = `${idPrefix}-${kind}-${v.replace(/[^a-z0-9]/gi, "")}`;
    const checked = value[kind].includes(v);
    // The last kind can't be unticked: with none, nothing would be listed.
    const lastKind = kind === "primary" && checked && value.primary.length === 1;
    return (
      <label key={v} htmlFor={id} className="flex items-center gap-1.5 text-sm" title={HINTS[v]}>
        <input
          id={id}
          type="checkbox"
          checked={checked}
          disabled={disabled || lastKind}
          onChange={(e) => onChange({ ...value, [kind]: toggle(value[kind], v, e.target.checked) })}
        />
        {v}
      </label>
    );
  };
  return (
    <div className="flex flex-col gap-3">
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className={`mb-1 ${FIELD_LABEL}`}>Kinds</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">{choices.primary.map((v) => box("primary", v))}</div>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className={`mb-1 ${FIELD_LABEL}`}>Also allow</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">{choices.secondary.map((v) => box("secondary", v))}</div>
      </fieldset>
      <p className="m-0 text-xs text-ink-faint">
        A release is listed when its kind is ticked and every extra type it has is ticked too: Album with Live lists live albums, not live singles
        or live compilations. Nothing ticked under Also allow means studio releases only.
      </p>
    </div>
  );
}

/** sameTypes reports whether two type sets list the same releases (order aside). */
export function sameTypes(a: ReleaseTypes, b: ReleaseTypes): boolean {
  const key = (t: ReleaseTypes) => [...t.primary].sort().join(",") + "|" + [...t.secondary].sort().join(",");
  return key(a) === key(b);
}
