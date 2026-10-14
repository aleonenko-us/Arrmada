import { describe, expect, it } from "vitest";
import { LINKS } from "../../lib/links";
import { SEARCH_INDEX, SECTIONS, searchSettings, sectionFromPath, settingsRedirect, visibleSections } from "./sections";

const admin = visibleSections(true).map((s) => s.id);
const manager = visibleSections(false).map((s) => s.id);

describe("visibleSections", () => {
  it("gives a manager what the old Media and Library tabs showed, plus Connections, Subtitles, Convert, Alerts (read-only) and the Plex connection", () => {
    expect(manager).toEqual(["library", "media", "connections", "subtitles", "convert", "alerts", "plex"]);
  });

  it("gives an admin every section, Status included", () => {
    expect(admin).toEqual(SECTIONS.map((s) => s.id));
    expect(admin).toContain("status");
  });
});

describe("settingsRedirect", () => {
  it("leaves a section the viewer can open alone", () => {
    expect(settingsRedirect("/settings/system", "", "#backups", admin)).toBeNull();
    expect(settingsRedirect("/settings/connections", "", "#indexer-12", manager)).toBeNull();
    expect(settingsRedirect("/settings/library", "", "", manager)).toBeNull();
  });

  it("splits the old Subtitles & Convert section by its card anchor", () => {
    expect(settingsRedirect("/settings/tools", "", "#subtitles", manager)).toBe("/settings/subtitles#subtitles");
    expect(settingsRedirect("/settings/tools", "", "#convert", manager)).toBe("/settings/convert#convert");
    expect(settingsRedirect("/settings/tools", "", "", admin)).toBe("/settings/subtitles");
    expect(settingsRedirect("/settings/tools", "?x=1", "#convert", admin)).toBe("/settings/convert?x=1#convert");
  });

  it("opens Library for bare /settings", () => {
    expect(settingsRedirect("/settings", "", "", admin)).toBe("/settings/library");
    expect(settingsRedirect("/settings/", "", "", manager)).toBe("/settings/library");
  });

  it("sends every old tab to its section", () => {
    expect(settingsRedirect("/settings", "?tab=media", "", admin)).toBe("/settings/media");
    expect(settingsRedirect("/settings", "?tab=library", "", admin)).toBe("/settings/library");
    expect(settingsRedirect("/settings", "?tab=system", "", admin)).toBe("/settings/system");
    expect(settingsRedirect("/settings", "?tab=users", "", admin)).toBe("/settings/users");
  });

  it("follows an old card anchor to the section that now holds it", () => {
    expect(settingsRedirect("/settings", "?tab=system", "#api-keys", admin)).toBe("/settings/connections#api-keys");
    expect(settingsRedirect("/settings", "?tab=system", "#disk-guard", admin)).toBe("/settings/downloads#disk-guard");
    expect(settingsRedirect("/settings", "?tab=system", "#recycle-bin", admin)).toBe("/settings/downloads#recycle-bin");
    expect(settingsRedirect("/settings", "?tab=library", "#media-folders", admin)).toBe("/settings/library#media-folders");
    expect(settingsRedirect("/settings", "", "#users", admin)).toBe("/settings/users#users");
  });

  it("sends a moved card's old address to the section that holds it now", () => {
    // CFG-15: the API keys and the Discovery region moved from System to Connections.
    expect(settingsRedirect("/settings/system", "", "#api-keys", admin)).toBe("/settings/connections#api-keys");
    expect(settingsRedirect("/settings/system", "", "#discovery-region", admin)).toBe("/settings/connections#discovery-region");
    expect(settingsRedirect("/settings", "?tab=system", "#api-keys", manager)).toBe("/settings/connections#api-keys");
  });

  it("follows a card that moved and was renamed: the Plex connection is the Plex card on Connections", () => {
    // CFG-16: /settings/plex#plex-connection (the health Fix link, old bookmarks).
    expect(settingsRedirect("/settings/plex", "", "#plex-connection", admin)).toBe("/settings/connections#plex");
    expect(settingsRedirect("/settings/plex", "", "#plex-connection", manager)).toBe("/settings/connections#plex");
    expect(settingsRedirect("/settings/plex", "?x=1&tab=system", "#plex-connection", admin)).toBe("/settings/connections?x=1#plex");
    // Its new home and the rest of Settings → Plex stay put.
    expect(settingsRedirect("/settings/connections", "", "#plex", manager)).toBeNull();
    expect(settingsRedirect("/settings/plex", "", "#plex-library-updates", manager)).toBeNull();
    expect(settingsRedirect("/settings/plex", "", "", manager)).toBeNull();
  });

  it("keeps other query params", () => {
    expect(settingsRedirect("/settings", "?tab=users&x=1", "", admin)).toBe("/settings/users?x=1");
  });

  it("falls back for a section the viewer can't open or that doesn't exist", () => {
    expect(settingsRedirect("/settings/system", "", "", manager)).toBe("/settings/library");
    expect(settingsRedirect("/settings/system", "", "#backups", manager)).toBe("/settings/library#backups");
    expect(settingsRedirect("/settings/nope", "", "", admin)).toBe("/settings/library");
  });
});

describe("LINKS into Settings", () => {
  // Every deep link copy uses must name a section and a card that exist.
  it.each([LINKS.apiKeys, LINKS.connections, LINKS.diskGuard, LINKS.recycleBin, LINKS.libraryFolders, LINKS.libraryReview, LINKS.users, LINKS.alerts, LINKS.plexConnection, LINKS.plex, LINKS.plexMonitoring, LINKS.plexSignIn, LINKS.tautulliImport, LINKS.audiobookshelfImport, LINKS.subtitlesSettings, LINKS.convertSettings, LINKS.audiobookServer])("%s resolves", (link) => {
    const [path, anchor] = link.split("#");
    const section = sectionFromPath(path);
    expect(admin).toContain(section);
    expect(settingsRedirect(path, "", anchor ? `#${anchor}` : "", admin)).toBeNull();
    if (anchor) expect(SEARCH_INDEX.find((e) => e.anchor === anchor)?.section).toBe(section);
  });
});

describe("searchSettings", () => {
  const labels = (q: string, visible: readonly string[] = admin) => searchSettings(q, visible, visible === admin).map((e) => e.label);

  it("finds the recycle bin, Plex sign-in and the naming cards", () => {
    expect(labels("recycle")).toEqual(["Recycle bin"]);
    expect(labels("plex")).toContain("Plex sign-in");
    expect(labels("plex path mapping")).toEqual(["Plex library updates"]);
    // The section name counts too, so "naming" also lists the rest of Naming & metadata.
    expect(labels("naming")).toEqual(["Plex recommended naming", "Movie naming", "Series naming", "Metadata"]);
  });

  it("needs every word typed, in any case", () => {
    expect(labels("SERIES episode")).toEqual(["Series naming"]);
    expect(labels("recycle tautulli")).toEqual([]);
    expect(labels("   ")).toEqual([]);
  });

  it("hides admin-only cards from a manager", () => {
    expect(labels("recycle", manager)).toEqual([]);
    // Managers keep the Plex library updates card (scans and mappings are day-to-day).
    expect(labels("plex", manager)).toEqual(["Plex recommended naming", "Metadata", "Other connections", "Plex connection", "Plex server", "Watch monitoring", "Plex library updates"]);
  });

  it("has a unique anchor per card", () => {
    const anchors = SEARCH_INDEX.map((e) => e.anchor);
    expect(new Set(anchors).size).toBe(anchors.length);
  });
});
