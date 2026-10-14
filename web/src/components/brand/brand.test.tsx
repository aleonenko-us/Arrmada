// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ServerBadge, WatchPills, safeWatchURL } from ".";

// The Watch links later lanes put on request pages, notices and Discover: one per server,
// the first as the accent button, the rest as brand pills; only http(s) links render.

afterEach(cleanup);

describe("WatchPills", () => {
  it("draws one link per server, the first as the main button", () => {
    render(<WatchPills links={[
      { server: "jellyfin", url: "https://jf.example.com/web/#/details?id=abc&serverId=s1" },
      { server: "plex", url: "https://app.plex.tv/desktop#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F7" },
    ]} />);
    const links = screen.getAllByRole("link");
    expect(links.map((a) => a.textContent)).toEqual(["▶Watch on Jellyfin", "Watch on Plex"]);
    expect(screen.getByRole("link", { name: /Watch on Jellyfin/ }).className).toContain("bg-accent-grad");
    const plex = screen.getByRole("link", { name: /Watch on Plex/ });
    expect(plex.className).not.toContain("bg-accent-grad");
    expect(plex.style.background).toBe("var(--plex)");
    expect(plex.style.color).toBe("var(--plex-ink)");
    for (const a of links) {
      expect(a.getAttribute("target")).toBe("_blank");
      expect(a.getAttribute("rel")).toBe("noopener noreferrer");
    }
  });

  it("drops a link that isn't http(s), and draws nothing when none is left", () => {
    const { container, rerender } = render(<WatchPills links={[
      { server: "plex", url: "javascript:alert(1)" },
      { server: "jellyfin", url: "https://jf.example.com/web/#/details?id=abc" },
    ]} />);
    expect(screen.getAllByRole("link")).toHaveLength(1);
    // The surviving link is now the first, so it's the main button.
    expect(screen.getByRole("link", { name: /Watch on Jellyfin/ }).className).toContain("bg-accent-grad");
    rerender(<WatchPills links={[{ server: "plex", url: "data:text/html,hi" }]} />);
    expect(container.innerHTML).toBe("");
  });

  it("names a server it doesn't know by the link's own label", () => {
    render(<WatchPills links={[
      { server: "plex", url: "https://plex.example.com/x" },
      { server: "emby", label: "Emby", url: "https://emby.example.com/x" },
    ]} />);
    expect(screen.getByRole("link", { name: /Watch on Emby/ })).toBeTruthy();
  });
});

describe("ServerBadge", () => {
  it("names the server on its brand fill, with the mark hidden from screen readers", () => {
    render(<ServerBadge server="jellyfin" />);
    const badge = screen.getByText("Jellyfin");
    expect(badge.style.background).toContain("var(--jellyfin)");
    expect(badge.style.color).toBe("var(--jellyfin-ink)");
    expect(badge.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });
});

describe("safeWatchURL", () => {
  it("keeps http and https only", () => {
    expect(safeWatchURL("http://192.168.1.2:8096/web/")).toBe("http://192.168.1.2:8096/web/");
    expect(safeWatchURL("https://jf.example.com")).toBe("https://jf.example.com/");
    expect(safeWatchURL("javascript:void(0)")).toBeNull();
    expect(safeWatchURL("not a url")).toBeNull();
    expect(safeWatchURL("")).toBeNull();
  });
});
