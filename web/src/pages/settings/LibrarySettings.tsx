import { useId } from "react";
import { Link } from "react-router-dom";
import { LanguagePicker } from "../../components/LanguagePicker";
import { AutoSelect, AutoToggle, SavedTick } from "../../components/settings/auto";
import { Section } from "../../components/settings/ui";
import { useMe } from "../../lib/me";
import { useAutoSave } from "../../lib/useAutoSave";
import { useLanguages } from "../../lib/languages";
import { useLoadedSettings, useSetting, useSettingGroup } from "../../lib/useSettings";
import type { AppSettings } from "../../lib/api";
import { Field } from "../../ui/Field";
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

type DisplayMode = AppSettings["display_title_mode"];

const DISPLAY_MODES: readonly { value: DisplayMode; label: string }[] = [
  { value: "english", label: "English (TMDB default)" },
  { value: "original", label: "Original title" },
  { value: "language", label: "My language" },
];

const DISPLAY_KEYS = ["display_title_mode", "display_title_language"] as const;

// DisplayTitles: which of a film's or show's names the app shows — TMDB's English title
// (the default), its original title in its own language and script, or its title in a
// chosen language — plus the original languages whose works always show their original
// title. Presentation only: matching, file and folder names and the API's title keep the
// English one. The mode and its language save together (a language mode needs one).
function DisplayTitles() {
  const id = useId();
  const langs = useLanguages();
  const group = useSettingGroup(DISPLAY_KEYS, {
    // "My language" stays picked while its language is still to choose.
    keepOnError: true,
    validate: (v) => (v.display_title_mode === "language" && !v.display_title_language ? "Pick the language to show titles in." : null),
  });
  const originals = useSetting("display_title_original_in", { equal: sameLangs, keepOnError: false });
  const mode = group.value.display_title_mode;
  const change = (next: Partial<Pick<AppSettings, (typeof DISPLAY_KEYS)[number]>>) => {
    const v = { ...group.value, ...next };
    group.set(v);
    void group.commit(v);
  };
  return (
    <div className="flex flex-col gap-4">
      <Field label="Show titles as" controlId={id} hint="Library pages, the calendar, Activity, alerts, requests and Discover cards for titles you have. File and folder names, matching and the API keep TMDB's English title.">
        <span className="flex flex-wrap items-center gap-2">
          <select id={id} value={mode} onChange={(e) => change({ display_title_mode: e.target.value as DisplayMode })} className="w-full max-w-[280px] rounded-lg border border-line bg-panel-2 px-3 py-2 text-base text-ink">
            {DISPLAY_MODES.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
          {mode === "language" && (
            <select aria-label="Language to show titles in" value={group.value.display_title_language} onChange={(e) => change({ display_title_language: e.target.value })} className="w-full max-w-[220px] rounded-lg border border-line bg-panel-2 px-3 py-2 text-base text-ink">
              <option value="">Pick a language…</option>
              {(langs ?? []).map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
            </select>
          )}
          <SavedTick save={group} />
        </span>
      </Field>
      {mode === "language" && (
        <span className="text-sm text-ink-faint">A title made in that language shows its original title. One TMDB has no translation for shows its original title. Descriptions are in that language too where TMDB has one, else in English.</span>
      )}
      {mode !== "original" && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-base font-semibold">Original titles for these languages</span>
            <SavedTick save={originals} />
          </div>
          <span className="text-sm text-ink-faint">
            Films and shows originally in these languages show their original title whatever the setting above — Greek films as Ο Θίασος while everything else stays in English.
          </span>
          <LanguagePicker value={originals.value} onChange={(next) => void originals.commit(next)} label="Add an original language" />
        </div>
      )}
      <span className="text-sm text-ink-faint">
        Changing this reads the names it needs for your whole library, a little at a time. Any title can have its own on its page: ⋯ → Display title….
      </span>
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
      <Section id="display-titles" title="Display titles" subtitle="Which name the app shows for each film and show.">
        <DisplayTitles />
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
