import { useState } from "react";
import { OrganizeModal } from "../../components/OrganizeModal";
import { AutoText, AutoToggle } from "../../components/settings/auto";
import { Preview, Section, TokenList } from "../../components/settings/ui";
import { Button, useConfirm, useToast } from "../../ui";
import { renderNaming, seriesIdFormat } from "../../lib/naming";
import { useSetting } from "../../lib/useSettings";

// Sample release used for the live naming preview.
const SAMPLE = {
  title: "Blade Runner 2049",
  year: "2017",
  quality: "2160p BluRay",
  resolution: "2160p",
  source: "BluRay",
  edition: "Director's Cut",
  plexedition: "{edition-Director's Cut}",
  codec: "x265",
  group: "FraMeSToR",
  tmdbid: "335984",
  imdbid: "tt1856101",
};

const TOKENS = ["title", "year", "quality", "resolution", "source", "edition", "plexedition", "codec", "group", "tmdbid", "imdbid"];

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
  tmdbid: "136315",
  tvdbid: "403245",
  imdbid: "tt14452776",
};
const SERIES_TOKENS = ["title", "year", "season", "season00", "episode", "episodetitle", "quality", "resolution", "source", "codec", "group", "tmdbid", "tvdbid", "imdbid"];

// Plex's recommended naming: ids in the folder names (what Plex matches on), the edition
// in the movie file name. A series without a TVDB id gets its TMDB id instead.
const PLEX_PRESET = {
  movieFolder: "{title} ({year}) {tmdb-{tmdbid}}",
  movieFile: "{title} ({year}) {edition-{edition}} - {quality}",
  seriesFolder: "{title} ({year}) {tvdb-{tvdbid}}",
};

const render = (format: string) => renderNaming(format, SAMPLE);
const renderSeries = (format: string) => renderNaming(seriesIdFormat(format, SERIES_SAMPLE), SERIES_SAMPLE);

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
  const confirm = useConfirm();
  const toast = useToast();
  const [applying, setApplying] = useState(false);

  const isPlex = movieFolder.value === PLEX_PRESET.movieFolder && movieFile.value === PLEX_PRESET.movieFile && seriesFolder.value === PLEX_PRESET.seriesFolder;
  const usePlexPreset = async () => {
    const ok = await confirm({
      title: "Use Plex’s recommended naming?",
      body: "The movie folder, movie file and series folder templates are replaced. Existing folders only change when you rename them.",
      confirmLabel: "Use Plex recommended",
    });
    if (!ok) return;
    setApplying(true);
    try {
      const saved = await Promise.all([
        movieFolder.commit(PLEX_PRESET.movieFolder),
        movieFile.commit(PLEX_PRESET.movieFile),
        seriesFolder.commit(PLEX_PRESET.seriesFolder),
      ]);
      if (saved.every(Boolean)) toast("Naming set to Plex recommended.");
    } finally {
      setApplying(false);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <Section id="naming-presets" title="Presets" subtitle="Start from a known-good scheme, then adjust any template below.">
        <div className="flex flex-wrap items-center gap-3">
          <Button size="sm" onClick={usePlexPreset} busy={applying} busyLabel="Saving…" disabled={isPlex}>
            {isPlex ? "Using Plex recommended" : "Plex recommended"}
          </Button>
          <span className="text-[11px] text-ink-faint">
            Ids in folder names so Plex matches the right title, and editions in movie file names. Existing folders only change when you rename them.
          </span>
        </div>
      </Section>
      <Section id="movie-naming" title="Movie naming" subtitle="How imported movie files are named. Tokens are replaced per release.">
        <AutoText label="Folder name" save={movieFolder}><Preview>{render(movieFolder.value)}</Preview></AutoText>
        <AutoText label="File name" save={movieFile}><Preview>{render(movieFile.value)}.mkv</Preview></AutoText>
        <TokenList tokens={TOKENS} />
        <p className="text-[11px] text-ink-faint">
          <code className="font-mono">{"{plexedition}"}</code> is Plex’s <code className="font-mono">{"{edition-Director's Cut}"}</code>, and nothing when there’s no edition. A version with an edition always gets it in its file name, so Plex shows each version as an edition of one movie. Write <code className="font-mono">{"{tmdb-{tmdbid}}"}</code> for Plex’s id tag; an empty tag drops out of the name. A movie that already has a folder keeps it until you rename it.
        </p>
        <OrganizeEntry />
      </Section>
      <Section id="series-naming" title="Series naming" subtitle="How imported episodes are named: the show folder holds season folders, which hold episode files.">
        <AutoText label="Series folder" save={seriesFolder}><Preview>{renderSeries(seriesFolder.value)}</Preview></AutoText>
        <AutoText label="Season folder" save={seasonFolder}><Preview>{renderSeries(seasonFolder.value)}</Preview></AutoText>
        <AutoText label="Episode file" save={episodeFile}><Preview>{renderSeries(episodeFile.value)}.mkv</Preview></AutoText>
        <p className="text-[11px] text-ink-faint">
          <code className="font-mono">{"{episode}"}</code> is the SxxExx tag (a range for double episodes). Use <code className="font-mono">{"{season00}"}</code> for a zero-padded season number. Season 0 is always “Specials”. <code className="font-mono">{"{tvdb-{tvdbid}}"}</code> falls back to the TMDB id for a show TVDB doesn’t know. A show that already has a folder keeps it.
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
