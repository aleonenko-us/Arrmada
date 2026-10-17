import { Link } from "react-router-dom";
import { LanguagePicker } from "../../components/LanguagePicker";
import { AutoSelect, AutoToggle, SavedTick } from "../../components/settings/auto";
import { Section } from "../../components/settings/ui";
import { useMe } from "../../lib/me";
import { useAutoSave } from "../../lib/useAutoSave";
import { useLoadedSettings, useSetting } from "../../lib/useSettings";
import { LibraryFolders } from "../Library";
import { ExtraFolders } from "./ExtraFolders";
import { DEFAULT_MONITOR_PRESET, MONITOR_PRESETS, isMonitorPreset, type MonitorPreset } from "../series/presets";

// Settings → Library: where each library lives on disk, and what happens when a title is
// added. Managers see this section too; LibraryFolders makes the folders read-only for them.
// Every control saves itself; the folders save per row when one is picked or confirmed.
function SeriesMonitorDefault() {
  const { s, commit } = useLoadedSettings();
  // A stored value that isn't a preset reads as the default, as the server does.
  const current: MonitorPreset = isMonitorPreset(s.series_monitor_default) ? s.series_monitor_default : DEFAULT_MONITOR_PRESET;
  const save = useAutoSave<MonitorPreset>(current, (v) => commit({ series_monitor_default: v }), { keepOnError: false });
  return (
    <AutoSelect label="Monitor" save={save} options={MONITOR_PRESETS}>
      <span className="text-xs text-ink-faint">{MONITOR_PRESETS.find((p) => p.value === save.value)?.help}</span>
    </AutoSelect>
  );
}

const sameLangs = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

// BookLanguages: the languages automatic book grabs accept. A release that says it's in
// another language is skipped by the sweep and RSS, and marked in 'Search indexers'.
function BookLanguages() {
  const save = useSetting("books_languages", { equal: sameLangs, keepOnError: false });
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-base font-semibold">Book languages</span>
        <SavedTick save={save} />
      </div>
      <span className="text-sm text-ink-faint">
        Automatic grabs skip a release that says it's in another language; 'Search indexers' still lists it so you can pick it by hand. Releases that don't say are always allowed. Leave the list empty to accept any language.
      </span>
      <LanguagePicker value={save.value} onChange={(next) => void save.commit(next)} label="Add a book language" />
    </div>
  );
}

// TitleLanguages: the extra languages a folder or a download Arrmada didn't grab may be
// named in ("Der Pate (1972)" for The Godfather), and that TV searches also find a show's
// releases by ("Haus des Geldes" for Money Heist). Empty keeps matching to English and
// original titles; a grab for a title always imports under any of its names.
function TitleLanguages() {
  const save = useSetting("match_title_languages", { equal: sameLangs, keepOnError: false });
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-base font-semibold">Also match titles in</span>
        <SavedTick save={save} />
      </div>
      <span className="text-sm text-ink-faint">
        Library scans and downloads Arrmada didn't grab also recognise a title by its name in these languages, and TV searches also look for and take a show's releases by it when the release also says the language, or the show's year or country. A name another title already goes by, or one two shows share, is never matched. Adding a language reads the titles in it for your whole library, a little at a time; TV searches start using them once every show's are read.
      </span>
      <LanguagePicker value={save.value} onChange={(next) => void save.commit(next)} label="Add a title language" />
    </div>
  );
}

export function LibrarySettings() {
  const searchOnAdd = useSetting("search_on_add");
  const { booksEnabled } = useMe();
  return (
    <div className="flex flex-col gap-6">
      <Section id="media-folders" title="Media folders" subtitle="Where each library lives on disk.">
        {/* People look here first for a second Movies or TV folder (GitHub #3). */}
        <p className="m-0 text-sm text-ink-dim">
          Have separate folders for kids, anime…?{" "}
          <a href="#more-folders" className="font-semibold" style={{ color: "var(--accent-text)" }}>Add them under More Movies and TV folders below ↓</a>
        </p>
        <LibraryFolders />
      </Section>
      <Section id="more-folders" title="More Movies and TV folders" subtitle="Keep kids', animation or anime titles in folders of their own — often their own Plex or Jellyfin library. New titles go to the first folder whose rule matches, else the main folder.">
        <ExtraFolders />
      </Section>
      <Section id="adding-titles" title="Adding titles" subtitle="Defaults when adding movies and series.">
        <AutoToggle label="Search on add" hint="Start searching for a release as soon as a title is added." save={searchOnAdd} />
        <Link to="/quality" className="text-sm font-semibold" style={{ color: "var(--accent-text)" }}>Quality profiles →</Link>
      </Section>
      <Section id="series-monitoring" title="Series monitoring" subtitle="Which episodes a new series monitors. The add dialog starts from this, and requests use it.">
        <SeriesMonitorDefault />
      </Section>
      <Section id="title-languages" title="Titles in other languages" subtitle="For folders, downloads and TV releases named in your household's language.">
        <TitleLanguages />
      </Section>
      {booksEnabled && (
        <Section id="books" title="Books" subtitle="How automatic searches choose ebook and audiobook releases.">
          <BookLanguages />
        </Section>
      )}
    </div>
  );
}
