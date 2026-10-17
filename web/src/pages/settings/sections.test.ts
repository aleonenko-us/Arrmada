import { describe, expect, it } from "vitest";
import { LINKS } from "../../lib/links";
import { SEARCH_INDEX, SECTIONS, searchSettings, sectionFromPath, settingsRedirect, visibleSections } from "./sections";

const admin = visibleSections(true).map((s) => s.id);
const manager = visibleSections(false).map((s) => s.id);

describe("visibleSections", () => {
  it("gives a manager what the old Media and Library tabs showed, plus Connections, Subtitles, Convert, Alerts (read-only) and Media servers", () => {
    expect(manager).toEqual(["library", "media", "connections", "subtitles", "convert", "alerts", "media-servers"]);
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

  it("sends the old Outgoing email card under Alerts to Send to Kindle email on Book server", () => {
    expect(settingsRedirect("/settings/alerts", "", "#outgoing-email", admin)).toBe("/settings/audiobook-server#outgoing-email");
    // A manager can't open Book server, and never saw the card: Alerts stays put.
    expect(settingsRedirect("/settings/alerts", "", "#outgoing-email", manager)).toBeNull();
    expect(SECTIONS.find((s) => s.id === "audiobook-server")?.label).toBe("Book server");
    expect(searchSettings("kindle", admin, true).map((e) => e.label)).toEqual(["Send to Kindle email"]);
  });

  it("follows a card that moved and was renamed: the Plex connection is the Plex card on Connections", () => {
    // CFG-16: /settings/plex#plex-connection (the health Fix link, old bookmarks).
    expect(settingsRedirect("/settings/plex", "", "#plex-connection", admin)).toBe("/settings/connections#plex");
    expect(settingsRedirect("/settings/plex", "", "#plex-connection", manager)).toBe("/settings/connections#plex");
    expect(settingsRedirect("/settings/plex", "?x=1&tab=system", "#plex-connection", admin)).toBe("/settings/connections?x=1#plex");
    // ARRIMPORT: the Radarr/Sonarr folders card became the library import.
    expect(settingsRedirect("/settings/import", "", "#arr-folders", admin)).toBe("/settings/import#arr-import");
    // Its new home stays put.
    expect(settingsRedirect("/settings/connections", "", "#plex", manager)).toBeNull();
  });

  it("sends the old Plex section and every one of its cards to Media servers, same anchor", () => {
    // The Plex section became Media servers (Plex and Jellyfin side by side).
    expect(settingsRedirect("/settings/plex", "", "", manager)).toBe("/settings/media-servers");
    expect(settingsRedirect("/settings/plex", "", "", admin)).toBe("/settings/media-servers");
    for (const anchor of ["plex-server", "plex-monitoring", "plex-library-updates", "plex-tautulli"]) {
      expect(settingsRedirect("/settings/plex", "", `#${anchor}`, manager)).toBe(`/settings/media-servers#${anchor}`);
    }
    for (const anchor of ["plex-sign-in", "plex-accounts", "jellyfin-sign-in"]) {
      expect(settingsRedirect("/settings/plex", "", `#${anchor}`, admin)).toBe(`/settings/media-servers#${anchor}`);
    }
    // plex.tv's return address from an older build keeps its query (the page passes the PIN on).
    expect(settingsRedirect("/settings/plex", "?plexpin=7", "", admin)).toBe("/settings/media-servers?plexpin=7");
    // The new page and its cards stay put.
    expect(settingsRedirect("/settings/media-servers", "", "", manager)).toBeNull();
    expect(settingsRedirect("/settings/media-servers", "", "#jellyfin-library-updates", manager)).toBeNull();
    expect(settingsRedirect("/settings/media-servers", "", "#plex-sign-in", admin)).toBeNull();
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
  it.each([LINKS.apiKeys, LINKS.connections, LINKS.diskGuard, LINKS.recycleBin, LINKS.libraryFolders, LINKS.libraryReview, LINKS.users, LINKS.alerts, LINKS.plexConnection, LINKS.jellyfinConnection, LINKS.mediaServerCards, LINKS.mediaServers, LINKS.plexServer, LINKS.jellyfinServer, LINKS.plexSignIn, LINKS.jellyfinSignIn, LINKS.tautulliImport, LINKS.audiobookshelfImport, LINKS.subtitlesSettings, LINKS.convertSettings, LINKS.audiobookServer])("%s resolves", (link) => {
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
    // n8n both hears from Arrmada (Alerts) and calls it (a personal API key).
    expect(labels("n8n")).toEqual(["Alerts", "Personal API keys"]);
    expect(labels("api key")).toEqual(expect.arrayContaining(["Personal API keys", "API keys"]));
    expect(labels("webhook")).toContain("Alerts");
    expect(labels("plex")).toContain("Plex sign-in");
    expect(labels("plex path mapping")).toEqual(["Plex library updates"]);
    expect(labels("jellyfin path mapping")).toEqual(["Jellyfin library updates"]);
    expect(labels("jellyfin public address")).toEqual(["Jellyfin connection", "Jellyfin Watch links"]);
    expect(labels("quick connect")).toEqual(["Jellyfin sign-in"]);
    // The section name counts too, so "naming" also lists the rest of Naming & metadata.
    expect(labels("naming")).toEqual(["Plex recommended naming", "Movie naming", "Series naming", "Book naming", "Metadata"]);
  });

  it("needs every word typed, in any case", () => {
    expect(labels("SERIES episode")).toEqual(["Series naming"]);
    expect(labels("recycle tautulli")).toEqual([]);
    expect(labels("   ")).toEqual([]);
  });

  it("hides admin-only cards from a manager", () => {
    expect(labels("recycle", manager)).toEqual([]);
    // Managers keep the Plex library updates card (scans and mappings are day-to-day).
    expect(labels("plex", manager)).toEqual(["Plex recommended naming", "Metadata", "Media servers", "Plex connection", "Plex server", "Plex watch monitoring", "Plex library updates"]);
    // Jellyfin's sign-in card is an admin's, like Plex's.
    expect(labels("jellyfin quick", manager)).toEqual([]);
    expect(labels("jellyfin quick")).toEqual(["Jellyfin sign-in"]);
  });

  it("has a unique anchor per card", () => {
    const anchors = SEARCH_INDEX.map((e) => e.anchor);
    expect(new Set(anchors).size).toBe(anchors.length);
  });
});
