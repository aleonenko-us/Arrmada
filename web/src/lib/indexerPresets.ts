// The add-indexer catalogue (Settings → Connections → + Add indexer). Each preset is the
// form a kind of tracker needs: which fields it shows, the name it gets, what it's used
// for by default and the paragraph that explains it. Presets carry only URLs and
// categories: a key, password or session is always typed by the owner into the form.
//
// It's static data, but it's only read by the lazy AddIndexerModal, so it downloads with
// that dialog rather than with the Connections page.

/** The inputs a preset's form shows. Name, tie-break and minimum seeders are always there
 *  under "More options"; seed rules always show. */
export type PresetField = "url" | "api_key" | "username" | "password" | "mam_session";

export interface IndexerPreset {
  /** Stable id for the tile (and tests). */
  id: string;
  /** The tile's title. */
  label: string;
  /** Two letters on the tile: no third-party logos. */
  monogram: string;
  /** One line under the title. */
  blurb: string;
  /** The indexer kind it creates. "prowlarr" isn't one: that tile opens the Prowlarr card. */
  kind: "torrentleech" | "myanonamouse" | "1337x" | "torznab" | "prowlarr";
  /** The name a new indexer gets until the owner types their own. */
  defaultName: string;
  fields: PresetField[];
  /** Areas it's searched for by default; empty = every area. */
  mediaTypes: string[];
  /** Placeholder for the URL field. */
  urlPlaceholder?: string;
  /** Whether the URL can be left blank (1337x falls back to its default site). */
  urlOptional?: boolean;
  /** The help paragraph under the form. */
  help: string;
  /** A private tracker counts how long you seed, so the seed rules get a reminder. */
  privateTracker?: boolean;
}

export const INDEXER_PRESETS: IndexerPreset[] = [
  {
    id: "torrentleech",
    label: "TorrentLeech",
    monogram: "TL",
    blurb: "Private tracker · movies and TV",
    kind: "torrentleech",
    defaultName: "TorrentLeech",
    fields: ["username", "password"],
    mediaTypes: [],
    help: "Native, no Prowlarr or Jackett needed. Your login is stored on your server. Uses FlareSolverr for Cloudflare. New uploads are checked every 15 minutes. 2FA isn't supported yet.",
    privateTracker: true,
  },
  {
    id: "myanonamouse",
    label: "MyAnonaMouse",
    monogram: "MA",
    blurb: "Private tracker · books and audiobooks",
    kind: "myanonamouse",
    defaultName: "MyAnonaMouse",
    fields: ["mam_session"],
    mediaTypes: ["book"],
    help: "Native books and audiobooks. In MyAnonaMouse go to Preferences → Security → Create session, allow your server's IP (ASN-locked is fine) and paste the mam_id here. Narrator, author, series, language and format come straight from MAM. If MAM is also in Prowlarr, remove it there to avoid duplicate results.",
    privateTracker: true,
  },
  {
    id: "1337x",
    label: "1337x",
    monogram: "13",
    blurb: "Public tracker · movies and TV",
    kind: "1337x",
    defaultName: "1337x",
    fields: ["url"],
    mediaTypes: ["movie", "series"],
    urlPlaceholder: "https://1337x.to (leave blank for the default)",
    urlOptional: true,
    help: "Public tracker: no login, no ratio. Uses FlareSolverr to get past Cloudflare and hands qBittorrent a magnet.",
  },
  {
    id: "prowlarr",
    label: "Prowlarr",
    monogram: "PR",
    blurb: "Bring in every indexer Prowlarr has",
    kind: "prowlarr",
    defaultName: "Prowlarr",
    fields: [],
    mediaTypes: [],
    help: "Opens the Prowlarr card, which pulls Prowlarr's indexers in and keeps them in sync hourly.",
  },
  {
    id: "torznab",
    label: "Jackett / other Torznab",
    monogram: "TZ",
    blurb: "Any Torznab feed by URL and key",
    kind: "torznab",
    defaultName: "Torznab",
    fields: ["url", "api_key"],
    mediaTypes: [],
    urlPlaceholder: "http://jackett:9117/api/v2.0/indexers/<id>/results/torznab/",
    help: "A Torznab feed from Jackett or anything else that speaks it. Copy the feed URL and API key from Jackett's indexer list. Test reads what the feed supports, so searches use its ids and categories.",
  },
];

export const presetById = (id: string) => INDEXER_PRESETS.find((p) => p.id === id);

/** The seed rules a new indexer starts with: the server's own default for new rows. */
export const DEFAULT_SEED_DAYS = 14;
