import { test, expect } from "./mockApi";

// OBS-10/11/13/14, SEC-08: alerts live in Settings → Alerts. Old addresses land there,
// saved links show only a hint, events come from the catalog in groups, each card says
// how its last alert went, and managers can look but not change anything.
test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("old addresses land on Settings → Alerts, and Insights has no Notifications tab", async ({ page }) => {
    await page.goto("/notifications");
    await expect(page).toHaveURL(/\/settings\/alerts$/);
    await page.goto("/insights?tab=notifications");
    await expect(page).toHaveURL(/\/settings\/alerts$/);
    // The Plex connection left Insights too: its old tab lands on the Plex card on
    // Settings → Connections, open to connect it.
    await page.goto("/insights?tab=settings");
    await expect(page).toHaveURL(/\/settings\/connections#plex$/);
    await expect(page.locator("#plex").getByRole("button", { name: "Sign in with Plex" })).toBeVisible();
    await page.goto("/insights");
    await expect(page.getByRole("tab", { name: "Activity" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Notifications" })).toHaveCount(0);
  });

  test("connections show a hint, grouped events, delivery status and the log", async ({ page, api }) => {
    await page.goto("/settings/alerts");
    const discord = page.locator("div.rounded-xl", { has: page.locator('input[value="Family Discord"]') }).last();
    await expect(discord.getByLabel("Link")).toHaveAttribute("placeholder", /discord:\/\/••••OKEN — saved, leave blank to keep/);
    await expect(discord.getByLabel("Link")).toHaveValue("");
    await expect(discord.getByText(/Last alert failed: apprise: exit status 1/)).toBeVisible();
    await expect(discord.getByText("Library", { exact: true })).toBeVisible();
    await expect(discord.getByLabel("Movie imported")).toBeChecked();
    await expect(discord.getByLabel("Episodes imported")).not.toBeChecked();
    // Groups with nothing in them yet aren't shown.
    await expect(discord.getByText("Needs you", { exact: true })).toHaveCount(0);

    await discord.getByRole("button", { name: "Recent deliveries" }).click();
    await expect(discord.getByText("📥 The Cartographer")).toBeVisible();

    await discord.getByRole("button", { name: "Test" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/notifications/1/test").length).toBe(1);
    // A saved card's Test never sends the link from the browser.
    expect(api.callsTo("POST", "/api/v1/notifications/test")).toHaveLength(0);

    await expect(page.getByText("Web Push to every device the account turned push on for.")).toBeVisible();
    await expect(page.getByText("On iPhone, push works only when Arrmada is added to the Home Screen.")).toBeVisible();
    // A connection made before the forms is a custom (raw link) one.
    await expect(discord.getByText("Custom (Apprise URL)")).toBeVisible();
  });

  // OBS-16: "Add alert" asks where alerts go; Discord needs only the pasted webhook, and
  // the page sends the form's fields — the server builds the link.
  test("Add alert offers presets and sends a Discord webhook as a field", async ({ page, api }) => {
    await page.goto("/settings/alerts");
    await page.getByRole("button", { name: "+ Add alert" }).click();
    await expect(page.getByText("Where should alerts go?")).toBeVisible();
    await page.getByRole("button", { name: /^Discord/ }).click();
    await expect(page.getByText("Discord: Server Settings → Integrations → Webhooks → Copy URL.")).toBeVisible();
    // A textbox: the saved n8n card's Webhook URL is kept ("•••• saved", a Replace button).
    const webhook = page.getByRole("textbox", { name: "Webhook URL" });
    await expect(webhook).toHaveAttribute("type", "password");
    await webhook.fill("https://discord.com/api/webhooks/1/test-token");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/notifications").filter((c) => c.path === "/api/v1/notifications").length).toBe(1);
    const sent = api.callsTo("POST", "/api/v1/notifications").find((c) => c.path === "/api/v1/notifications")!.body as Record<string, unknown>;
    expect(sent).toMatchObject({ kind: "discord", name: "Discord", fields: { webhook_url: "https://discord.com/api/webhooks/1/test-token" } });
    expect(sent).not.toHaveProperty("url");
  });

  // n8n / Webhook: the page makes the signing secret and shows it until it's saved; the
  // auth fields follow the chosen authentication; Add sends them all as fields.
  test("Add alert → n8n / Webhook makes a signing secret and sends the fields", async ({ page, api }) => {
    await page.goto("/settings/alerts");
    await page.getByRole("button", { name: "+ Add alert" }).click();
    await page.getByRole("button", { name: /^n8n \/ Webhook/ }).click();
    // The new card (the only one with Add); the saved n8n card has the same fields.
    const card = page.locator("div.rounded-xl", { has: page.getByRole("button", { name: "Add", exact: true }) }).last();
    const secret = card.getByLabel("Signing secret");
    await expect(secret).toHaveValue(/^[A-Za-z0-9_-]{43}$/);
    const first = await secret.inputValue();
    await expect(card.getByText("Copy it into n8n now: once saved it’s never shown again.")).toBeVisible();
    await card.getByRole("button", { name: "Make a new one" }).click();
    await expect(secret).not.toHaveValue(first);

    await expect(card.getByLabel("Header name")).toHaveCount(0);
    await card.getByLabel("Authentication").selectOption("header");
    await card.getByLabel("Header name").fill("X-Arrmada-Key");
    await card.getByLabel("Header value").fill("a-long-random-value");
    await card.getByLabel("Webhook URL").fill("http://192.168.1.10:5678/webhook/arrmada");
    // No wording to customise: a webhook gets JSON.
    await expect(card.getByRole("button", { name: /Customise message/ })).toHaveCount(0);
    await card.getByRole("button", { name: "Add", exact: true }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/notifications").filter((c) => c.path === "/api/v1/notifications").length).toBe(1);
    const sent = api.callsTo("POST", "/api/v1/notifications").find((c) => c.path === "/api/v1/notifications")!.body as { kind: string; fields: Record<string, string> };
    expect(sent.kind).toBe("n8n");
    expect(sent.fields).toMatchObject({ url: "http://192.168.1.10:5678/webhook/arrmada", auth: "header", header_name: "X-Arrmada-Key", header_value: "a-long-random-value" });
    expect(sent.fields.signing_secret).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(sent.fields.signing_secret).not.toBe(first);
  });

  test("a saved n8n connection keeps its secrets hidden and sends an event's sample as its test", async ({ page, api }) => {
    await page.goto("/settings/alerts");
    const card = page.locator("div.rounded-xl", { has: page.locator('input[value="Home n8n"]') }).last();
    await expect(card.getByText("n8n / Webhook")).toBeVisible();
    // URL, header value and signing secret: saved, never shown.
    await expect(card.getByText("•••• saved")).toHaveCount(3);
    await expect(card.getByLabel("Header name")).toHaveValue("X-Arrmada-Key");
    await expect(card.getByLabel("Event to send as a test")).toHaveValue("movie.imported");
    await card.getByLabel("Event to send as a test").selectOption("episodes.imported");
    await card.getByRole("button", { name: "Send test" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/notifications/4/test").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/notifications/4/test")[0].body).toEqual({ event: "episodes.imported" });
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("sees the connections read-only", async ({ page }) => {
    await page.goto("/settings/alerts");
    await expect(page.getByText("Only an admin can add or change alert connections.")).toBeVisible();
    await expect(page.getByText("discord://••••OKEN")).toBeVisible();
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "+ Add alert" })).toHaveCount(0);
  });
});
