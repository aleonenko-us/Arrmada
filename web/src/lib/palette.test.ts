import { describe, expect, it } from "vitest";
import {
  actionItems, highlight, loadRecents, moveActive, pageItems, pushRecent, rank, results, score,
  type LibraryHit, type PaletteItem, type PaletteViewer,
} from "./palette";

const admin: PaletteViewer = { role: "admin", external: false, booksEnabled: true, musicEnabled: false };
const manager: PaletteViewer = { ...admin, role: "manager" };
const requester: PaletteViewer = { ...admin, role: "requester" };
const outside: PaletteViewer = { ...requester, external: true };

const labels = (items: readonly PaletteItem[]) => items.map((i) => i.label);
const ranked = (q: string, v: PaletteViewer = admin) => rank(q, pageItems(v)).map((r) => r.item.label);

describe("score", () => {
  it("prefers the whole label, then its start, a word, the words in any order, then keywords", () => {
    expect(score("calendar", "Calendar")).toBe(100);
    expect(score("cal", "Calendar")).toBe(90);
    expect(score("naming", "Movie naming")).toBe(80);
    expect(score("naming movie", "Movie naming")).toBe(70);
    expect(score("aming", "Movie naming")).toBe(60);
    expect(score("homepage", "Dashboard widget", "homepage gethomepage")).toBe(50);
    expect(score("dshb", "Dashboard")).toBe(20);
    expect(score("zzz", "Dashboard")).toBe(0);
    expect(score("   ", "Dashboard")).toBe(0);
  });

  it("ignores accents, case and & against and", () => {
    expect(score("amelie", "Amélie")).toBe(100);
    expect(score("POKÉMON", "Pokemon")).toBe(100);
    expect(score("fast and furious", "Fast & Furious")).toBe(100);
  });
});

describe("rank", () => {
  it("jumps straight to the settings card people ask for", () => {
    expect(ranked("two-factor")[0]).toBe("Two-factor sign-in");
    expect(ranked("homepage")[0]).toBe("Dashboard widget");
    expect(ranked("naming")[0]).toBe("Book naming");
    expect(ranked("naming")).toEqual(expect.arrayContaining(["Movie naming", "Series naming", "Settings → Naming & metadata"]));
  });

  it("puts a page whose name was typed ahead of a card that only mentions it", () => {
    expect(ranked("calendar")[0]).toBe("Calendar");
    expect(ranked("dashboard")[0]).toBe("Dashboard");
  });
});

describe("pageItems by role", () => {
  it("gives a requester their own pages and Me, never the console or Settings", () => {
    const items = pageItems(requester);
    expect(labels(items)).toEqual(expect.arrayContaining(["Discover", "Requests", "Calendar", "My shelf", "Audiobooks", "Me", "Password and two-factor"]));
    expect(items.some((i) => i.to?.startsWith("/settings"))).toBe(false);
    expect(items.some((i) => i.to === "/movies" || i.to === "/activity/queue" || i.to === "/logs")).toBe(false);
    // Two-factor for a requester is their own Account card.
    expect(ranked("two-factor", requester)[0]).toBe("Password and two-factor");
  });

  it("leaves Calendar and the calendar feed out away from home, and the shelf when Books is off", () => {
    const items = pageItems({ ...outside, booksEnabled: false });
    expect(items.some((i) => i.to === "/calendar" || i.to === "/me#calendar-feed")).toBe(false);
    expect(items.some((i) => i.to === "/shelf" || i.to === "/me#kindle")).toBe(false);
  });

  it("keeps admin-only pages, sections and cards from a manager", () => {
    const a = pageItems(admin).map((i) => i.to);
    const m = pageItems(manager).map((i) => i.to);
    for (const to of ["/logs", "/settings/system", "/settings/users#two-factor", "/settings/media-servers#plex-sign-in", "/settings/status#updates"]) {
      expect(a).toContain(to);
      expect(m).not.toContain(to);
    }
    // Managers keep their sections and cards.
    expect(m).toEqual(expect.arrayContaining(["/settings/library", "/settings/media#movie-naming", "/settings/media-servers#plex-library-updates"]));
  });

  it("follows the module switches", () => {
    expect(pageItems({ ...admin, musicEnabled: true }).some((i) => i.to === "/music")).toBe(true);
    expect(pageItems(admin).some((i) => i.to === "/music")).toBe(false);
    expect(pageItems({ ...admin, booksEnabled: false }).some((i) => i.to === "/books")).toBe(false);
  });

  it("never lists the Settings search's own palette entry as a card", () => {
    expect(pageItems(admin).some((i) => i.to?.includes("search-everything"))).toBe(false);
  });
});

describe("actionItems", () => {
  it("offers staff the everyday actions and nothing destructive", () => {
    expect(labels(actionItems(admin))).toEqual(["Add movie", "Add series", "Scan library", "Check for updates", "Open Activity → Needs you", "Sign out"]);
    expect(labels(actionItems(manager))).toEqual(["Add movie", "Add series", "Scan library", "Open Activity → Needs you", "Sign out"]);
    for (const a of actionItems(admin)) expect(a.label).not.toMatch(/delete|remove|empty|purge|reset|restore/i);
  });

  it("offers a requester only Sign out", () => {
    expect(labels(actionItems(requester))).toEqual(["Sign out"]);
  });
});

const hits: LibraryHit[] = [
  { kind: "movie", id: 1, title: "Dune", year: 2021, poster_url: "https://image.tmdb.org/t/p/w500/d.jpg" },
  { kind: "book", id: 4, title: "Dune Messiah", subtitle: "Frank Herbert", year: 1969 },
];

describe("results", () => {
  const base = (v: PaletteViewer, query: string) => ({ query, viewer: v, pages: pageItems(v), actions: actionItems(v), library: hits, discover: [{ media_type: "movie" as const, tmdb_id: 438631, title: "Dune", year: 2021 }], discoverReady: true });

  it("groups library, pages and Discover, best group first and Discover last", () => {
    const s = results(base(admin, "dune"));
    expect(s.map((x) => x.group)).toEqual(["library", "discover"]);
    expect(s[0].items[0]).toMatchObject({ label: "Dune", hint: "Movie · 2021", to: "/movies/1" });
    expect(s[0].items[1]).toMatchObject({ hint: "Book · Frank Herbert · 1969", to: "/books/4" });
    expect(s[1].items[0]).toMatchObject({ label: "Search Discover for “dune”", to: "/discover?q=dune" });
    expect(s[1].items[1]).toMatchObject({ to: "/discover/movie/438631" });

    // A page named exactly what was typed comes before a title that contains it.
    const cal = results({ ...base(admin, "calendar"), library: [{ kind: "movie", id: 9, title: "The Calendar Man" }] });
    expect(cal.map((x) => x.group)).toEqual(["pages", "library", "discover"]);
  });

  it("never gives a requester a library group, whatever is passed in", () => {
    const s = results(base(requester, "dune"));
    expect(s.map((x) => x.group)).toEqual(["discover"]);
    expect(s.flatMap((x) => x.items).some((i) => i.group === "library")).toBe(false);
  });

  it("leaves Discover out when it isn't set up", () => {
    const s = results({ ...base(admin, "dune"), discoverReady: false });
    expect(s.map((x) => x.group)).toEqual(["library"]);
  });

  it("lists recent picks, the main pages and the actions before anything is typed", () => {
    const recent: PaletteItem = { id: "page:/calendar", group: "pages", label: "Calendar", to: "/calendar", top: true };
    const s = results({ ...base(admin, ""), recents: [recent] });
    expect(s.map((x) => x.group)).toEqual(["recent", "pages", "actions"]);
    expect(s[1].items.some((i) => i.id === "page:/calendar")).toBe(false); // not twice
    expect(s[1].items.some((i) => i.to?.startsWith("/settings/") && i.to.includes("#"))).toBe(false); // cards wait for a query
  });
});

describe("recents", () => {
  const memory = () => {
    const m = new Map<string, string>();
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => { m.set(k, v); } };
  };

  it("are per account, newest first, without repeats", () => {
    const store = memory();
    const pages = pageItems(admin);
    const cal = pages.find((p) => p.to === "/calendar")!;
    const logs = pages.find((p) => p.to === "/logs")!;
    pushRecent(1, cal, store);
    pushRecent(1, logs, store);
    pushRecent(1, cal, store);
    expect(loadRecents(1, admin, pages, store).map((r) => r.to)).toEqual(["/calendar", "/logs"]);
    expect(loadRecents(2, admin, pages, store)).toEqual([]);
  });

  it("drop what the viewer may no longer open", () => {
    const store = memory();
    const pages = pageItems(admin);
    pushRecent(1, pages.find((p) => p.to === "/logs")!, store);
    pushRecent(1, { id: "movie:1", group: "library", label: "Dune", to: "/movies/1" }, store);
    pushRecent(1, { id: "tmdb:movie:5", group: "discover", label: "Heat", to: "/discover/movie/5" }, store);
    // The same account as a manager: no Logs. As a requester: no library title either.
    expect(loadRecents(1, manager, pageItems(manager), store).map((r) => r.id)).toEqual(["tmdb:movie:5", "movie:1"]);
    expect(loadRecents(1, requester, pageItems(requester), store).map((r) => r.id)).toEqual(["tmdb:movie:5"]);
  });

  it("skip the Discover search row and sign-out, and survive junk in storage", () => {
    const store = memory();
    pushRecent(1, { id: "discover:search", group: "discover", label: "Search", to: "/discover?q=x" }, store);
    pushRecent(1, actionItems(admin).find((a) => a.action === "sign-out")!, store);
    expect(loadRecents(1, admin, [], store)).toEqual([]);
    store.setItem("arrmada.palette.recent.1", "{not json");
    expect(loadRecents(1, admin, [], store)).toEqual([]);
    store.setItem("arrmada.palette.recent.1", JSON.stringify([{ id: "x", group: "library", label: "Evil", to: "https://evil.example/" }]));
    expect(loadRecents(1, admin, [], store)).toEqual([]);
  });
});

describe("highlight", () => {
  it("marks the typed text, accents and case aside", () => {
    expect(highlight("Amélie", "ame")).toEqual([{ text: "Amé", hit: true }, { text: "lie", hit: false }]);
    expect(highlight("Movie naming", "naming movie").filter((p) => p.hit).map((p) => p.text)).toEqual(["Movie", "naming"]);
    expect(highlight("Calendar", "")).toEqual([{ text: "Calendar", hit: false }]);
  });
});

describe("moveActive", () => {
  it("moves with the arrows and wraps at both ends", () => {
    expect(moveActive(0, 3, "ArrowDown")).toBe(1);
    expect(moveActive(2, 3, "ArrowDown")).toBe(0);
    expect(moveActive(0, 3, "ArrowUp")).toBe(2);
    expect(moveActive(1, 3, "Home")).toBe(0);
    expect(moveActive(1, 3, "End")).toBe(2);
    expect(moveActive(1, 0, "ArrowDown")).toBe(-1);
  });
});
