import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { NOW } from "./fixtures/clock";

// Personal API keys: made on the Me page (every role), shown once with a copy button,
// listed by name and hint only, and revoked; the admin sees everyone's in People & access.

const KEY = "arrk_" + "abcdefghijklmnopqrstuvwxyz234567".repeat(2).slice(0, 48) + "7f3q";

interface Key {
  id: number; name: string; hint: string; scope: "read" | "full"; role: string;
  created_at: string; last_used_at: string | null; last_used_network?: string;
  expires_at: string | null; expired: boolean; user_id?: number; username?: string;
}

// keyServer stands in for the server's keys: a list that POST adds to (only with the right
// password) and DELETE removes from, recording every call like the default fixtures.
async function keyServer(page: Page, api: MockedApi, start: Key[] = [], flags: { code_required?: boolean; reauth_required?: boolean } = {}) {
  const keys = [...start];
  await page.route((u) => u.pathname.startsWith("/api/v1/me/api-keys"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    let body: Record<string, unknown> | undefined;
    try { body = req.postDataJSON(); } catch { body = undefined; }
    api.calls.push({ method: req.method(), path: url.pathname, body });
    const json = (status: number, out: unknown) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(out) });
    if (req.method() === "GET") return json(200, { keys, expiry_days: [0, 30, 90, 365], max: 25, password_required: !flags.reauth_required, ...flags });
    if (req.method() === "POST") {
      if (body?.password !== "password123") return json(400, { status: "error", message: "Your password is wrong" });
      if (flags.code_required && body?.code !== "123456") return json(400, { status: "error", message: "That code isn't right" });
      const days = Number(body.expires_in_days);
      const k: Key = {
        id: keys.length + 10, name: String(body.name), hint: "arrk_…" + KEY.slice(-4), scope: body.scope as Key["scope"], role: "requester",
        created_at: new Date(NOW).toISOString(), last_used_at: null,
        expires_at: days ? new Date(NOW + days * 86_400_000).toISOString() : null, expired: false,
      };
      keys.unshift(k);
      return json(201, { key: KEY, api_key: k });
    }
    if (req.method() === "DELETE") {
      const id = Number(url.pathname.split("/").pop());
      const i = keys.findIndex((k) => k.id === id);
      if (i < 0) return json(404, { status: "error", message: "no such key" });
      keys.splice(i, 1);
      return route.fulfill({ status: 204 });
    }
    return route.fallback();
  });
}

const card = (page: Page) => page.getByRole("region", { name: "Personal API keys" });

test.describe("Me → Personal API keys", () => {
  test.use({ persona: "requester" });

  test("make a key, copy it once, see it listed by its hint, revoke it", async ({ page, api, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await keyServer(page, api);
    await page.goto("/me");
    await card(page).getByRole("button", { name: "New API key" }).click();

    const form = card(page).getByRole("form", { name: "New API key" });
    await form.getByLabel("Name").fill("n8n");
    await form.getByLabel("Access").selectOption("full");
    await form.getByLabel("Expires").selectOption("30");
    // The password is asked for; a wrong one says so and makes nothing.
    await form.getByLabel("Your password").fill("guess");
    await form.getByRole("button", { name: "Make key" }).click();
    await expect(form.getByRole("alert")).toHaveText("Your password is wrong");
    await form.getByLabel("Your password").fill("password123");
    await form.getByRole("button", { name: "Make key" }).click();
    expect(api.callsTo("POST", "/api/v1/me/api-keys").at(-1)?.body).toEqual({ name: "n8n", scope: "full", expires_in_days: 30, password: "password123" });

    // Shown once, with a copy button that puts it on the clipboard.
    const shown = card(page).getByRole("textbox", { name: "API key" });
    await expect(shown).toHaveValue(KEY);
    await card(page).getByRole("button", { name: "Copy key" }).click();
    await expect(card(page).getByRole("button", { name: "Copied" })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(KEY);

    // Listed by name and hint, never the key.
    const list = card(page).getByRole("list", { name: "Your API keys" });
    const row = list.getByRole("listitem").filter({ hasText: "n8n" });
    await expect(row).toContainText("arrk_…7f3q");
    await expect(row).toContainText("Full");
    await expect(row).toContainText("Never used");
    await expect(row).toContainText("Expires in 30 days");

    // Done: the key is gone from the page, and a reload never brings it back.
    await card(page).getByRole("button", { name: "Done" }).click();
    await expect(shown).toHaveCount(0);
    await expect(page.locator(`text=${KEY}`)).toHaveCount(0);
    await page.reload();
    await expect(card(page).getByRole("listitem").filter({ hasText: "n8n" })).toBeVisible();
    expect(await page.content()).not.toContain(KEY);

    // Revoke, after confirming.
    await card(page).getByRole("button", { name: "Revoke n8n" }).click();
    const dialog = page.getByRole("dialog", { name: "Revoke “n8n”?" });
    await dialog.getByRole("button", { name: "Revoke key" }).click();
    await expect(card(page).getByRole("listitem")).toHaveCount(0);
    expect(api.callsTo("DELETE", "/api/v1/me/api-keys/").map((c) => c.path)).toEqual(["/api/v1/me/api-keys/10"]);
  });
});

test.describe("Me → Personal API keys, stepping up", () => {
  test.use({ persona: "requester" });

  test("with two-factor on, making a key asks for a code as well", async ({ page, api }) => {
    await keyServer(page, api, [], { code_required: true });
    await page.goto("/me");
    await card(page).getByRole("button", { name: "New API key" }).click();
    const form = card(page).getByRole("form", { name: "New API key" });
    await form.getByLabel("Name").fill("n8n");
    await form.getByLabel("Your password").fill("password123");
    await form.getByLabel("Two-factor code").fill("654321");
    await form.getByRole("button", { name: "Make key" }).click();
    await expect(form.getByRole("alert")).toHaveText("That code isn't right");
    await form.getByLabel("Two-factor code").fill("123456");
    await form.getByRole("button", { name: "Make key" }).click();
    await expect(card(page).getByRole("textbox", { name: "API key" })).toHaveValue(KEY);
    expect(api.callsTo("POST", "/api/v1/me/api-keys").at(-1)?.body).toMatchObject({ password: "password123", code: "123456" });
  });

  test("a Plex-only account signed in a while ago is asked to sign in again", async ({ page, api }) => {
    await keyServer(page, api, [], { reauth_required: true });
    await page.goto("/me");
    await expect(card(page).getByText("keys can only be made within 15 minutes of signing in", { exact: false })).toBeVisible();
    await expect(card(page).getByRole("button", { name: "New API key" })).toHaveCount(0);
    await expect(card(page).getByRole("button", { name: "Sign out to sign in again" })).toBeVisible();
  });
});

test.describe("People & access → Personal API keys", () => {
  test.use({ persona: "admin" });

  test("the admin sees whose keys exist and revokes one", async ({ page, api }) => {
    const theirs: Key = {
      id: 4, name: "home assistant", hint: "arrk_…k2m9", scope: "read", role: "requester", user_id: 3, username: "deckhand",
      created_at: new Date(NOW - 5 * 86_400_000).toISOString(), last_used_at: new Date(NOW - 3_600_000).toISOString(),
      expires_at: null, expired: false,
    };
    let keys = [theirs];
    await page.route((u) => u.pathname.startsWith("/api/v1/users/api-keys"), async (route) => {
      const req = route.request();
      api.calls.push({ method: req.method(), path: new URL(req.url()).pathname, body: undefined });
      if (req.method() === "DELETE") { keys = []; return route.fulfill({ status: 204 }); }
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ keys }) });
    });
    await page.goto("/settings/users#personal-api-keys");
    const list = page.getByRole("list", { name: "Everyone’s API keys" });
    const row = list.getByRole("listitem").filter({ hasText: "deckhand" });
    await expect(row).toContainText("home assistant");
    await expect(row).toContainText("arrk_…k2m9");
    await expect(row).toContainText("Read only");
    await row.getByRole("button", { name: "Revoke deckhand’s key home assistant" }).click();
    await page.getByRole("dialog", { name: "Revoke deckhand’s key “home assistant”?" }).getByRole("button", { name: "Revoke key" }).click();
    await expect(page.getByRole("list", { name: "Everyone’s API keys" })).toHaveCount(0);
    await expect(page.getByText("Nobody has made one yet.")).toBeVisible();
    expect(api.callsTo("DELETE", "/api/v1/users/api-keys/").map((c) => c.path)).toEqual(["/api/v1/users/api-keys/4"]);
  });
});
