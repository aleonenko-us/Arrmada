import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mediaDetail, notifications, readyRequests } from "./fixtures/discover";
import type { MediaRequest } from "../src/lib/api";

// J3: a title on both media servers has a Watch button per server, the viewer's own
// first. The API orders them; these fixtures answer as it does for someone who watches
// on Jellyfin (Jellyfin, then Plex). Jellyfin's link is at the owner's public address.

const jellyfin = "https://watch.example.com/web/#/details?id=4071f1445812b3d8fba8a91b47dbb5fb&serverId=5e7e75e7e75e7e75e7e75e7e75e7e75e";
const plex = readyRequests.requests[0].plex_url ?? "";
const links = [
  { server: "jellyfin", label: "Jellyfin", url: jellyfin },
  { server: "plex", label: "Plex", url: plex },
];
const ready: MediaRequest = { ...readyRequests.requests[0], watch_links: links };

// onBoth answers the request, the title and the inbox with both servers' links (routes
// added last are matched first, so these win over the shared fixtures).
async function onBoth(page: Page) {
  const json = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  await page.route((u) => u.pathname === "/api/v1/requests", (r) => r.fulfill(json({ ...readyRequests, requests: [ready] })));
  await page.route((u) => u.pathname === `/api/v1/requests/${ready.id}`, (r) => r.fulfill(json({ request: ready, client_health: { ok: true } })));
  await page.route((u) => u.pathname === "/api/v1/media/movie/1007", (r) => r.fulfill(json({ ...mediaDetail("movie", 1007), plex_url: plex, watch_links: links })));
  await page.route((u) => u.pathname === "/api/v1/me/notifications", (r) =>
    r.fulfill(json({ ...notifications, notifications: notifications.notifications.map((n, i) => (i === 0 ? { ...n, watch_links: links } : n)) })));
}

test.describe("requester whose server is Jellyfin", () => {
  test.use({ persona: "requester" });

  test("the request sheet has both Watch buttons, Jellyfin first", async ({ page, api }) => {
    await onBoth(page);
    await page.goto(`/requests?tab=ready&id=${ready.id}`);
    const sheet = page.getByRole("dialog", { name: ready.title });
    await expect(sheet).toBeVisible();
    await api.quiet();
    const watch = sheet.getByRole("link", { name: /Watch on/ });
    await expect(watch).toHaveText([/Watch on Jellyfin/, /Watch on Plex/]);
    await expect(watch.nth(0)).toHaveAttribute("href", jellyfin);
    await expect(watch.nth(1)).toHaveAttribute("href", plex);
    await expect(watch.nth(0)).toHaveAttribute("target", "_blank");
    // The row behind it has room for one: the viewer's own server.
    await page.keyboard.press("Escape");
    await expect(page.getByRole("link", { name: "▶ Watch on Jellyfin" })).toHaveAttribute("href", jellyfin);
  });

  test("the title sheet has both Watch buttons, Jellyfin first", async ({ page, api }) => {
    await onBoth(page);
    await page.goto("/discover/movie/1007");
    const sheet = page.getByRole("dialog");
    await api.quiet();
    await expect(sheet.getByRole("link", { name: /Watch on/ })).toHaveText([/Watch on Jellyfin/, /Watch on Plex/]);
    await expect(sheet.getByRole("link", { name: /Watch on Jellyfin/ })).toHaveAttribute("href", jellyfin);
  });

  test("the inbox has both Watch buttons, Jellyfin first", async ({ page, api }) => {
    await onBoth(page);
    await page.goto("/discover");
    await api.quiet();
    await page.getByRole("button", { name: "Notifications" }).first().click();
    const watch = page.getByRole("link", { name: /^▶ Watch on/ });
    await expect(watch).toHaveText(["▶ Watch on Jellyfin", "▶ Watch on Plex"]);
    await expect(watch.nth(0)).toHaveAttribute("href", jellyfin);
  });

  test("a notice from an older server keeps its Plex button", async ({ page, api }) => {
    // The shared fixtures carry plex_url only, as a server from before Jellyfin sends.
    await page.goto("/discover");
    await api.quiet();
    await page.getByRole("button", { name: "Notifications" }).first().click();
    await expect(page.getByRole("link", { name: /^▶ Watch on/ })).toHaveText(["▶ Watch on Plex"]);
  });
});
