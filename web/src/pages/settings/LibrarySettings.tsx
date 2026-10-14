import { Link } from "react-router-dom";
import { LanguagePicker } from "../../components/LanguagePicker";
import { AutoSelect, AutoToggle, SavedTick } from "../../components/settings/auto";
import { Section } from "../../components/settings/ui";
import { useMe } from "../../lib/me";
import { useAutoSave } from "../../lib/useAutoSave";
import { useLoadedSettings, useSetting } from "../../lib/useSettings";
import { LibraryFolders } from "../Library";
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

export function LibrarySettings() {
  const searchOnAdd = useSetting("search_on_add");
  const { booksEnabled } = useMe();
  return (
    <div className="flex flex-col gap-6">
      <Section id="media-folders" title="Media folders" subtitle="Where each library lives on disk.">
        <LibraryFolders />
      </Section>
      <Section id="adding-titles" title="Adding titles" subtitle="Defaults when adding movies and series.">
        <AutoToggle label="Search on add" hint="Start searching for a release as soon as a title is added." save={searchOnAdd} />
        <Link to="/quality" className="text-sm font-semibold" style={{ color: "var(--accent-text)" }}>Quality profiles →</Link>
      </Section>
      <Section id="series-monitoring" title="Series monitoring" subtitle="Which episodes a new series monitors. The add dialog starts from this, and requests use it.">
        <SeriesMonitorDefault />
      </Section>
      {booksEnabled && (
        <Section id="books" title="Books" subtitle="How automatic searches choose ebook and audiobook releases.">
          <BookLanguages />
        </Section>
      )}
    </div>
  );
}
