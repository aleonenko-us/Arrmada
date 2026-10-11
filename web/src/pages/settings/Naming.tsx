import { useState } from "react";
import { OrganizeModal } from "../../components/OrganizeModal";
import { AutoText, AutoToggle } from "../../components/settings/auto";
import { Button } from "../../ui";
import { Preview, Section, TokenList } from "../../components/settings/ui";
import { useSetting } from "../../lib/useSettings";

// Sample release used for the live naming preview.
const SAMPLE = {
  title: "Blade Runner 2049",
  year: "2017",
  quality: "2160p BluRay",
  resolution: "2160p",
  source: "BluRay",
  edition: "Director's Cut",
  codec: "x265",
  group: "FraMeSToR",
};

const TOKENS = ["title", "year", "quality", "resolution", "source", "edition", "codec", "group"];

// Sample series episode for the series naming preview.
const SERIES_SAMPLE = {
  title: "The Bear",
  year: "2022",
  season: "4",
  season00: "04",
  episode: "S04E01",
  episodetitle: "Tomorrow",
  quality: "1080p WEB-DL",
  resolution: "1080p",
  source: "WEB-DL",
  codec: "x264",
  group: "NTb",
};
const SERIES_TOKENS = ["title", "year", "season", "season00", "episode", "episodetitle", "quality", "resolution", "source", "codec", "group"];

// renderWith mirrors the backend renderName tidy-up: substitute tokens, drop empty
// bracket pairs and stranded "- -" separators, strip illegal filename chars.
function renderWith(format: string, sample: Record<string, string>): string {
  let out = format;
  for (const [k, v] of Object.entries(sample)) out = out.split(`{${k}}`).join(v);
  out = out.replace(/\(\)/g, "").replace(/\[\]/g, "").replace(/\s+/g, " ");
  while (out.includes("- -")) out = out.replace("- -", "-");
  out = out.replace(/^[\s-]+|[\s-]+$/g, "");
  return out.replace(/[<>:"/\\|?*]/g, "");
}
const render = (format: string) => renderWith(format, SAMPLE);
const renderSeries = (format: string) => renderWith(format, SERIES_SAMPLE);

// OrganizeEntry: a new scheme only names new imports. This previews what it would do to
// the movies already in the library, and applies it from the same dialog.
function OrganizeEntry() {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex flex-wrap items-center gap-2.5 text-[11.5px] text-ink-faint">
      <Button size="sm" variant="secondary" onClick={() => setOpen(true)}>Preview rename…</Button>
      <span>Existing files keep their names until you rename them.</span>
      {open && <OrganizeModal onClose={() => setOpen(false)} />}
    </div>
  );
}

// A blank template would quietly fall back to the default, so say so instead of saving it.
const notBlank = (v: string) => (v.trim() === "" ? "A name template can't be empty. Esc puts the saved one back." : null);

// Settings → Naming & metadata (the old Media tab): how imported files are named, and the
// sidecars written next to them. Each template saves when you leave the field (or press
// Enter); the preview follows every keystroke.
export function NamingSettings() {
  const movieFolder = useSetting("naming_movie_folder", { validate: notBlank });
  const movieFile = useSetting("naming_movie_file", { validate: notBlank });
  const seriesFolder = useSetting("naming_series_folder", { validate: notBlank });
  const seasonFolder = useSetting("naming_series_season", { validate: notBlank });
  const episodeFile = useSetting("naming_series_episode", { validate: notBlank });
  const writeNFO = useSetting("write_nfo");
  const artwork = useSetting("download_artwork");
  return (
    <div className="flex flex-col gap-6">
      <Section id="movie-naming" title="Movie naming" subtitle="How imported movie files are named. Tokens are replaced per release.">
        <AutoText label="Folder name" save={movieFolder}><Preview>{render(movieFolder.value)}</Preview></AutoText>
        <AutoText label="File name" save={movieFile}><Preview>{render(movieFile.value)}.mkv</Preview></AutoText>
        <TokenList tokens={TOKENS} />
        <OrganizeEntry />
      </Section>
      <Section id="series-naming" title="Series naming" subtitle="How imported episodes are named: the show folder holds season folders, which hold episode files.">
        <AutoText label="Series folder" save={seriesFolder}><Preview>{renderSeries(seriesFolder.value)}</Preview></AutoText>
        <AutoText label="Season folder" save={seasonFolder}><Preview>{renderSeries(seasonFolder.value)}</Preview></AutoText>
        <AutoText label="Episode file" save={episodeFile}><Preview>{renderSeries(episodeFile.value)}.mkv</Preview></AutoText>
        <p className="text-[11px] text-ink-faint">
          <code className="font-mono">{"{episode}"}</code> is the SxxExx tag (a range for double episodes). Use <code className="font-mono">{"{season00}"}</code> for a zero-padded season number. Season 0 is always “Specials”.
        </p>
        <TokenList tokens={SERIES_TOKENS} />
      </Section>
      <Section id="metadata" title="Metadata" subtitle="Written into each movie folder for Plex, Jellyfin, Emby and Kodi.">
        <AutoToggle label="Write movie.nfo" hint="A metadata sidecar with title, plot, ids, ratings." save={writeNFO} />
        <AutoToggle label="Download artwork" hint="Save poster.jpg and fanart.jpg next to the movie." save={artwork} />
      </Section>
    </div>
  );
}
