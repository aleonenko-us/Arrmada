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
  /** Two letters on the tile, shown until (or instead of) the site's icon. */
  monogram: string;
  /** The site icon's slug (siteIcons.ts), when the site has one. */
  icon?: string;
  /** The address the URL field starts with (a known usenet indexer's API). */
  defaultUrl?: string;
  /** One line under the title. */
  blurb: string;
  /** The indexer kind it creates. "prowlarr" isn't one: that tile opens the Prowlarr card. */
  kind: "torrentleech" | "myanonamouse" | "1337x" | "torznab" | "newznab" | "prowlarr";
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
  /** A usenet indexer: offered only with a usenet download client, and nothing seeds. */
  usenet?: boolean;
}

export const INDEXER_PRESETS: IndexerPreset[] = [
  {
    id: "torrentleech",
    icon: "torrentleech",
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
    icon: "myanonamouse",
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
    icon: "1337x",
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
    icon: "prowlarr",
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
    icon: "jackett",
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
  {
    id: "nzbgeek",
    label: "NZBgeek",
    monogram: "NG",
    icon: "nzbgeek",
    blurb: "Usenet · popular, paid",
    kind: "newznab",
    defaultName: "NZBgeek",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://api.nzbgeek.info/api",
    urlPlaceholder: "https://api.nzbgeek.info/api",
    help: "Paste your API key from your NZBgeek profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "drunkenslug",
    label: "DrunkenSlug",
    monogram: "DS",
    icon: "drunkenslug",
    blurb: "Usenet · invite or open signups",
    kind: "newznab",
    defaultName: "DrunkenSlug",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://drunkenslug.com/api",
    urlPlaceholder: "https://drunkenslug.com/api",
    help: "Paste your API key from your DrunkenSlug profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "nzbfinder",
    label: "NZBFinder",
    monogram: "NF",
    icon: "nzbfinder",
    blurb: "Usenet · free tier",
    kind: "newznab",
    defaultName: "NZBFinder",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://nzbfinder.ws/api",
    urlPlaceholder: "https://nzbfinder.ws/api",
    help: "Paste your API key from your NZBFinder profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "nzbplanet",
    label: "NZBPlanet",
    monogram: "NP",
    icon: "nzbplanet",
    blurb: "Usenet · paid",
    kind: "newznab",
    defaultName: "NZBPlanet",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://api.nzbplanet.net/api",
    urlPlaceholder: "https://api.nzbplanet.net/api",
    help: "Paste your API key from your NZBPlanet profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "dognzb",
    label: "DOGnzb",
    monogram: "DG",
    icon: "dognzb",
    blurb: "Usenet · invite only",
    kind: "newznab",
    defaultName: "DOGnzb",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://api.dognzb.cr/api",
    urlPlaceholder: "https://api.dognzb.cr/api",
    help: "Paste your API key from your DOGnzb profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "althub",
    label: "altHUB",
    monogram: "AH",
    icon: "althub",
    blurb: "Usenet · free tier",
    kind: "newznab",
    defaultName: "altHUB",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://api.althub.co.za/api",
    urlPlaceholder: "https://api.althub.co.za/api",
    help: "Paste your API key from your altHUB profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "ninjacentral",
    label: "NinjaCentral",
    monogram: "NC",
    icon: "ninjacentral",
    blurb: "Usenet · paid",
    kind: "newznab",
    defaultName: "NinjaCentral",
    fields: ["url", "api_key"],
    mediaTypes: [],
    defaultUrl: "https://ninjacentral.co.za/api",
    urlPlaceholder: "https://ninjacentral.co.za/api",
    help: "Paste your API key from your NinjaCentral profile page; the address is filled in. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
  {
    id: "newznab",
    label: "Other Newznab indexer",
    monogram: "NZ",
    blurb: "Any usenet indexer by URL and key",
    kind: "newznab",
    defaultName: "Newznab",
    fields: ["url", "api_key"],
    mediaTypes: [],
    urlPlaceholder: "https://indexer.example/api",
    help: "A usenet indexer's API: its address (ending /api) and your API key, both on the indexer's profile page. What it finds goes to SABnzbd. Usenet doesn't seed, so there are no seed rules.",
    usenet: true,
  },
];

/** The catalogue as offered: the usenet tile only once a usenet download client exists. */
export function presetsFor(hasUsenetClient: boolean): IndexerPreset[] {
  return INDEXER_PRESETS.filter((p) => !p.usenet || hasUsenetClient);
}

/** The catalogue in two groups; usenet always shows, and says when it needs a client first. */
export const torrentPresets = () => INDEXER_PRESETS.filter((p) => !p.usenet);
export const usenetPresets = () => INDEXER_PRESETS.filter((p) => p.usenet);

export const presetById = (id: string) => INDEXER_PRESETS.find((p) => p.id === id);

/** The seed rules a new indexer starts with: the server's own default for new rows. */
export const DEFAULT_SEED_DAYS = 14;
