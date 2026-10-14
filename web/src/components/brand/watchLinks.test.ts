import { describe, expect, it } from "vitest";
import { firstWatch, watchLinksOf } from "./servers";

describe("watchLinksOf", () => {
  const jf = { server: "jellyfin", label: "Jellyfin", url: "https://watch.example.com/web/#/details?id=a" };
  const plex = { server: "plex", label: "Plex", url: "https://app.plex.tv/desktop/#!/x" };

  it("takes the server's links, in its order", () => {
    expect(watchLinksOf({ watch_links: [jf, plex], plex_url: plex.url })).toEqual([jf, plex]);
  });

  it("reads an older server's plex_url as the one Plex link", () => {
    expect(watchLinksOf({ plex_url: plex.url })).toEqual([plex]);
    expect(watchLinksOf({})).toEqual([]);
    expect(watchLinksOf(null)).toEqual([]);
  });

  it("gives a row the first usable link, the viewer's own server", () => {
    expect(firstWatch({ watch_links: [jf, plex] })).toEqual({ href: jf.url, label: "Jellyfin" });
    expect(firstWatch({ watch_links: [{ server: "jellyfin", url: "javascript:alert(1)" }, plex] })).toEqual({ href: plex.url, label: "Plex" });
    expect(firstWatch({})).toBeNull();
  });
});
