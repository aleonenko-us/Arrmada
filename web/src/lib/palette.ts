import type { UserRole } from "./api";
import { ACTIVITY_TAB_LABEL } from "./activity";
import { visibleNav } from "./nav";
import { requesterNav } from "./requesterNav";
import { SEARCH_INDEX, visibleSections } from "../pages/settings/sections";

// The command palette's brain, kept free of components so it can be tested on its own:
// what this person may jump to (pages, settings cards, actions), how a query ranks it, how
// the results group, and the recent picks. It loads with the palette's own chunk, never
// with a requester's first load. The library rows come from GET /api/v1/library/search (staff) and
// the Discover rows from the Discover search; both are only shaped here.

export type PaletteGroup = "recent" | "library" | "pages" | "actions" | "discover";

export const GROUP_LABEL: Record<PaletteGroup, string> = {
  recent: "Recent",
  library: "Library",
  pages: "Pages and settings",
  actions: "Actions",
  discover: "Discover",
};

/** Something the palette does besides (or instead of) opening an address. */
export type PaletteAction = "check-updates" | "sign-out";

export interface PaletteItem {
  /** Stable across sessions: recents are matched by it. */
  id: string;
  group: PaletteGroup;
  label: string;
  /** The grey line under the label: where it lives, or what kind of title it is. */
  hint?: string;
  /** Extra words that find it ("homepage" finds the dashboard widget card). */
  keywords?: string;
  /** Where choosing it goes. */
  to?: string;
  action?: PaletteAction;
  poster?: string;
  /** A main page: listed before anything is typed. */
  top?: boolean;
}

export interface PaletteViewer {
  role: UserRole;
  /** A requester away from home: no Calendar, like their shell. */
  external: boolean;
  booksEnabled: boolean;
  musicEnabled: boolean;
}

export const isStaffRole = (role: UserRole) => role === "admin" || role === "manager";

// ---- What can be jumped to -------------------------------------------------------------

// The Me page's cards, which everyone has (staff reach Me from the sidebar).
function meItems(v: PaletteViewer): PaletteItem[] {
  const me = (anchor: string, label: string, keywords: string): PaletteItem => ({ id: `me:${anchor}`, group: "pages", label, hint: "Me", keywords, to: `/me#${anchor}` });
  const out = [
    { id: "page:/me", group: "pages" as const, label: "Me", hint: "Page", keywords: "account profile you", to: "/me", top: true },
    me("notifications", "Get notified", "notifications push alerts phone apprise"),
    me("media-servers", "Your Plex and Jellyfin link", "plex jellyfin link account media server"),
  ];
  if (v.booksEnabled && v.role !== "readonly") out.push(me("kindle", "Send to Kindle", "kindle ebook email address"));
  if (!v.external) out.push(me("calendar-feed", "Calendar feed", "calendar feed ics google apple subscribe link"));
  out.push(
    me("account", "Password and two-factor", "password change two-factor two factor 2fa totp authenticator sign out other devices sessions security"),
    me("api-keys", "Personal API keys", "api key keys token script n8n home assistant"),
    { id: "page:/audiobooks?tab=apps", group: "pages", label: "Audiobook apps & password", hint: "Audiobooks", keywords: "listening apps lissen audiobook password", to: "/audiobooks?tab=apps" },
  );
  return out;
}

// pageItems: every page this person can open, settings sections and cards included for
// staff (the same role rules as the sidebar and the Settings hub).
export function pageItems(v: PaletteViewer): PaletteItem[] {
  if (!isStaffRole(v.role)) {
    const pages: PaletteItem[] = requesterNav({ external: v.external, booksEnabled: v.booksEnabled })
      .filter((n) => n.key !== "me")
      .map((n) => ({ id: `page:${n.to}`, group: "pages", label: n.label, hint: "Page", to: n.to, top: true }));
    if (v.booksEnabled) pages.push({ id: "page:/discover?tab=books", group: "pages", label: "Discover books", hint: "Discover", keywords: "books ebooks audiobooks", to: "/discover?tab=books" });
    return [...pages, ...meItems(v)];
  }
  const admin = v.role === "admin";
  const out: PaletteItem[] = [];
  for (const g of visibleNav({ admin, booksEnabled: v.booksEnabled, musicEnabled: v.musicEnabled })) {
    for (const n of g.items) out.push({ id: `page:${n.to}`, group: "pages", label: n.label, hint: g.group || "Page", to: n.to, top: true });
  }
  if (v.booksEnabled) out.push({ id: "page:/discover?tab=books", group: "pages", label: "Discover books", hint: "Discover", keywords: "books ebooks audiobooks", to: "/discover?tab=books" });
  out.push({ id: "page:/movies/wanted", group: "pages", label: "Wanted movies", hint: "Movies", keywords: "missing cutoff", to: "/movies/wanted" });
  // Activity's tabs, but Needs you: that's an action of its own below.
  for (const tab of ["queue", "wanted", "history", "blocklist"] as const) {
    out.push({ id: `page:/activity/${tab}`, group: "pages", label: `Activity → ${ACTIVITY_TAB_LABEL[tab]}`, hint: "Activity", keywords: "downloads", to: `/activity/${tab}` });
  }
  const sections = visibleSections(admin);
  const sectionLabel = new Map(sections.map((s) => [s.id, s.label]));
  for (const s of sections) {
    out.push({ id: `settings:${s.id}`, group: "pages", label: `Settings → ${s.label}`, hint: "Settings", to: `/settings/${s.id}` });
  }
  for (const e of SEARCH_INDEX) {
    const section = sectionLabel.get(e.section);
    if (!section || (e.adminOnly && !admin) || e.palette) continue;
    out.push({ id: `settings:${e.section}#${e.anchor}`, group: "pages", label: e.label, hint: `Settings → ${section}`, keywords: e.keywords, to: `/settings/${e.section}#${e.anchor}` });
  }
  return [...out, ...meItems(v)];
}

// actionItems: the few things staff do often. Each is a page's own button reached by its
// address (Add movie opens the Movies page's Add dialog) or a call the app already makes;
// nothing destructive is ever offered here.
export function actionItems(v: PaletteViewer): PaletteItem[] {
  const out: PaletteItem[] = [];
  if (isStaffRole(v.role)) {
    out.push(
      { id: "action:add-movie", group: "actions", label: "Add movie", hint: "Movies", keywords: "new film title", to: "/movies?add=1" },
      { id: "action:add-series", group: "actions", label: "Add series", hint: "Series", keywords: "new tv show title", to: "/series?add=1" },
      { id: "action:scan", group: "actions", label: "Scan library", hint: "Settings → Library", keywords: "scan rescan folders import existing find", to: "/settings/library#media-folders" },
    );
    if (v.role === "admin") out.push({ id: "action:check-updates", group: "actions", label: "Check for updates", hint: "Settings → Status", keywords: "update upgrade new version release", to: "/settings/status#updates", action: "check-updates" });
    out.push({ id: "action:needs-you", group: "actions", label: "Open Activity → Needs you", hint: "Activity", keywords: "needs you problems stuck held review imports", to: "/activity/needs-you" });
  }
  out.push({ id: "action:sign-out", group: "actions", label: "Sign out", keywords: "log out logout leave", action: "sign-out" });
  return out;
}

// ---- Matching ---------------------------------------------------------------------------

const MARKS = /[̀-ͯ]/g;

// fold is the palette's spelling-insensitive form: accents off, lower case, "&" as "and".
// (The library rows were matched on the server's own normaliser; this is for the pages,
// settings and actions, and for ordering what came back.)
export function fold(s: string): string {
  return s.normalize("NFD").replace(MARKS, "").toLowerCase().replace(/&/g, " and ").replace(/\s+/g, " ");
}

const wordsOf = (s: string) => s.split(/[^a-z0-9]+/).filter((w) => w && w !== "and");

// score is how well an item's label (and, more weakly, its keywords) matches the query;
// zero is no match. The whole label beats its start, which beats a word in it, which beats
// the words in any order, the letters anywhere, the keywords, and last the typed letters in
// order with gaps ("dshb" finds Dashboard).
export function score(query: string, label: string, keywords = ""): number {
  const q = fold(query).trim();
  if (!q) return 0;
  const l = fold(label).trim();
  if (l === q) return 100;
  if (l.startsWith(q)) return 90;
  const lw = wordsOf(l);
  const qw = wordsOf(q);
  if (qw.length === 0) return 0;
  if (lw.some((w) => w.startsWith(q))) return 80;
  if (qw.every((w) => lw.some((x) => x.startsWith(w)))) return 70;
  if (l.includes(q)) return 60;
  const kw = wordsOf(fold(keywords));
  if (qw.every((w) => lw.some((x) => x.startsWith(w)) || kw.some((x) => x.startsWith(w)))) return 50;
  const hay = `${l} ${fold(keywords)}`;
  if (qw.every((w) => hay.includes(w))) return 35;
  if (q.length >= 3 && subsequence(q.replace(/[^a-z0-9]/g, ""), l)) return 20;
  return 0;
}

// subsequence: every character of q appears in s in order, the first at the start of a word.
function subsequence(q: string, s: string): boolean {
  if (!q) return false;
  let at = -1;
  for (let i = 0; i < s.length; i++) {
    if (s[i] === q[0] && (i === 0 || !/[a-z0-9]/.test(s[i - 1]))) { at = i; break; }
  }
  if (at < 0) return false;
  let qi = 1;
  for (let i = at + 1; i < s.length && qi < q.length; i++) if (s[i] === q[qi]) qi++;
  return qi === q.length;
}

export interface Ranked { item: PaletteItem; score: number }

// rank keeps the items that match, best first; a tie goes to the shorter label (closer to
// what was typed), then to the original order.
export function rank(query: string, items: readonly PaletteItem[]): Ranked[] {
  return items
    .map((item, i) => ({ item, score: score(query, item.label, item.keywords), i }))
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score || a.item.label.length - b.item.label.length || a.i - b.i)
    .map(({ item, score: s }) => ({ item, score: s }));
}

// highlight splits a label into plain and matched pieces, for bolding what was typed: the
// whole query where it appears as one piece, otherwise each word's first appearance.
export function highlight(label: string, query: string): { text: string; hit: boolean }[] {
  // Fold character by character so positions in the folded text map back to the label.
  let folded = "";
  const at: number[] = [];
  for (let i = 0; i < label.length; i++) {
    const f = label[i].normalize("NFD").replace(MARKS, "").toLowerCase();
    for (let k = 0; k < f.length; k++) { folded += f[k]; at.push(i); }
  }
  const q = query.normalize("NFD").replace(MARKS, "").toLowerCase().trim();
  const marks = new Array<boolean>(label.length).fill(false);
  const mark = (start: number, len: number) => {
    for (let k = start; k < start + len; k++) marks[at[k]] = true;
  };
  if (q) {
    const whole = folded.indexOf(q);
    if (whole >= 0) mark(whole, q.length);
    else {
      for (const w of q.split(/\s+/).filter(Boolean)) {
        // Prefer the start of a word.
        const re = new RegExp(`(^|[^a-z0-9])${w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`);
        const m = re.exec(folded);
        if (m) mark(m.index + m[1].length, w.length);
        else {
          const i = folded.indexOf(w);
          if (i >= 0) mark(i, w.length);
        }
      }
    }
  }
  const out: { text: string; hit: boolean }[] = [];
  for (let i = 0; i < label.length; i++) {
    const last = out[out.length - 1];
    if (last && last.hit === marks[i]) last.text += label[i];
    else out.push({ text: label[i], hit: marks[i] });
  }
  return out;
}

// ---- Library and Discover rows ----------------------------------------------------------

/** One row of GET /api/v1/library/search. */
export interface LibraryHit {
  kind: "movie" | "series" | "book" | "artist" | "album";
  id: number;
  title: string;
  year?: number;
  subtitle?: string;
  poster_url?: string;
}

const KIND_LABEL: Record<LibraryHit["kind"], string> = { movie: "Movie", series: "Series", book: "Book", artist: "Artist", album: "Album" };
const KIND_PATH: Record<LibraryHit["kind"], (id: number) => string> = {
  movie: (id) => `/movies/${id}`,
  series: (id) => `/series/${id}`,
  book: (id) => `/books/${id}`,
  artist: (id) => `/music/${id}`,
  album: (id) => `/music/album/${id}`,
};

export function libraryItem(h: LibraryHit): PaletteItem {
  const hint = [KIND_LABEL[h.kind], h.subtitle, h.year ? String(h.year) : ""].filter(Boolean).join(" · ");
  return { id: `${h.kind}:${h.id}`, group: "library", label: h.title, hint, to: KIND_PATH[h.kind](h.id), poster: h.poster_url };
}

/** The part of a Discover search card the palette shows. */
export interface DiscoverHit { media_type: "movie" | "series"; tmdb_id: number; title: string; year?: number; poster_url?: string; in_library?: boolean }

export function discoverItem(c: DiscoverHit): PaletteItem {
  const hint = [c.media_type === "movie" ? "Movie" : "Series", c.year ? String(c.year) : "", c.in_library ? "In the library" : ""].filter(Boolean).join(" · ");
  return { id: `tmdb:${c.media_type}:${c.tmdb_id}`, group: "discover", label: c.title, hint, to: `/discover/${c.media_type}/${c.tmdb_id}`, poster: c.poster_url };
}

// discoverSearchItem is the row that always offers the full Discover search for the query.
export function discoverSearchItem(query: string): PaletteItem {
  const q = query.trim();
  return { id: "discover:search", group: "discover", label: `Search Discover for “${q}”`, hint: "Movies and TV from TMDB", to: `/discover?q=${encodeURIComponent(q)}` };
}

// ---- Grouping ---------------------------------------------------------------------------

export interface Section { group: PaletteGroup; items: PaletteItem[] }

// How many of each group show while typing.
const CAP: Record<PaletteGroup, number> = { recent: 5, library: 8, pages: 8, actions: 4, discover: 6 };

export interface ResultsInput {
  query: string;
  viewer: PaletteViewer;
  pages: readonly PaletteItem[];
  actions: readonly PaletteItem[];
  /** Library rows (staff only; ignored for anyone else). */
  library?: readonly LibraryHit[];
  /** Discover cards for the query, and whether Discover search is available at all. */
  discover?: readonly DiscoverHit[];
  discoverReady: boolean;
  recents?: readonly PaletteItem[];
}

// results is what the palette lists. Empty query: the recent picks, then every page and
// the actions. Typing: each group's matches, the group with the best match first (an exact
// page name beats a film that merely contains it), Discover always last since it's the
// way out to everything else. A requester never gets a Library group.
export function results(input: ResultsInput): Section[] {
  const { query, viewer } = input;
  const staff = isStaffRole(viewer.role);
  if (!query.trim()) {
    const recents = (input.recents ?? []).slice(0, CAP.recent);
    const seen = new Set(recents.map((r) => r.id));
    const pages = input.pages.filter((p) => p.top);
    const out: Section[] = [];
    if (recents.length) out.push({ group: "recent", items: recents });
    out.push({ group: "pages", items: pages.filter((p) => !seen.has(p.id)) });
    const actions = input.actions.filter((a) => !seen.has(a.id));
    if (actions.length) out.push({ group: "actions", items: actions });
    return out.filter((s) => s.items.length > 0);
  }

  const scored: { group: PaletteGroup; ranked: Ranked[] }[] = [];
  if (staff && input.library?.length) {
    // The server already matched and ordered these; score them here only to place the group.
    const ranked = input.library.slice(0, CAP.library).map((h) => {
      const item = libraryItem(h);
      return { item, score: Math.max(score(query, item.label), 30) };
    });
    scored.push({ group: "library", ranked });
  }
  const pages = rank(query, input.pages).slice(0, CAP.pages);
  if (pages.length) scored.push({ group: "pages", ranked: pages });
  const actions = rank(query, input.actions).slice(0, CAP.actions);
  if (actions.length) scored.push({ group: "actions", ranked: actions });
  // Stable sort: equal best scores keep Library, Pages, Actions order.
  scored.sort((a, b) => best(b.ranked) - best(a.ranked));
  const out: Section[] = scored.map((s) => ({ group: s.group, items: s.ranked.map((r) => r.item) }));
  if (input.discoverReady) {
    const cards = (input.discover ?? []).slice(0, CAP.discover - 1).map(discoverItem);
    out.push({ group: "discover", items: [discoverSearchItem(query), ...cards] });
  }
  return out;
}

const best = (r: Ranked[]) => r.reduce((m, x) => Math.max(m, x.score), 0);

// ---- Recent picks -----------------------------------------------------------------------

const RECENT_MAX = 8;
const recentKey = (userId: number) => `arrmada.palette.recent.${userId}`;

// loadRecents reads this person's recent picks (per account, so a shared browser never
// shows one person's picks to another) and keeps only what they may still open: a library
// title for staff only, a page or action only while it's still offered to them (in its
// current wording), a Discover title always (it's TMDB's public catalogue).
export function loadRecents(userId: number, viewer: PaletteViewer, offered: readonly PaletteItem[], store: Pick<Storage, "getItem"> | null = safeStorage()): PaletteItem[] {
  let raw: unknown = [];
  try { raw = JSON.parse(store?.getItem(recentKey(userId)) ?? "[]"); } catch { return []; }
  if (!Array.isArray(raw)) return [];
  const byId = new Map(offered.map((o) => [o.id, o]));
  const out: PaletteItem[] = [];
  for (const r of raw as Partial<PaletteItem>[]) {
    if (!r || typeof r.id !== "string" || typeof r.label !== "string") continue;
    let item: PaletteItem | undefined;
    if (r.group === "library") {
      if (isStaffRole(viewer.role) && typeof r.to === "string" && /^\/(movies|series|books|music)\//.test(r.to)) item = { id: r.id, group: "library", label: r.label, hint: r.hint, to: r.to, poster: r.poster };
    } else if (r.group === "discover") {
      if (typeof r.to === "string" && r.to.startsWith("/discover")) item = { id: r.id, group: "discover", label: r.label, hint: r.hint, to: r.to, poster: r.poster };
    } else {
      item = byId.get(r.id);
    }
    if (item && !out.some((o) => o.id === item.id)) out.push(item);
  }
  return out.slice(0, RECENT_MAX);
}

// pushRecent puts a pick at the front of this person's recent list. The "Search Discover
// for …" row and sign-out aren't worth remembering.
export function pushRecent(userId: number, item: PaletteItem, store: Pick<Storage, "getItem" | "setItem"> | null = safeStorage()): void {
  if (item.id === "discover:search" || item.action === "sign-out" || !store) return;
  try {
    const raw = JSON.parse(store.getItem(recentKey(userId)) ?? "[]") as PaletteItem[];
    const list = Array.isArray(raw) ? raw.filter((r) => r && r.id !== item.id) : [];
    const { id, group, label, hint, to, poster } = item;
    list.unshift({ id, group, label, hint, to, poster });
    store.setItem(recentKey(userId), JSON.stringify(list.slice(0, RECENT_MAX)));
  } catch { /* storage full or blocked: recents are a convenience */ }
}

function safeStorage(): Storage | null {
  try { return window.localStorage; } catch { return null; }
}

// ---- Keyboard ---------------------------------------------------------------------------

// moveActive is the next highlighted row for an arrow key, wrapping at both ends.
export function moveActive(active: number, count: number, key: string): number {
  if (count === 0) return -1;
  switch (key) {
    case "ArrowDown": return active < 0 || active >= count - 1 ? 0 : active + 1;
    case "ArrowUp": return active <= 0 ? count - 1 : active - 1;
    case "Home": return 0;
    case "End": return count - 1;
    default: return active;
  }
}
