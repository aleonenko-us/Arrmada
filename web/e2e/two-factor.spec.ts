import { createHmac } from "node:crypto";
import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { personas } from "./fixtures/users";
import { people, settings, status } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// Two-factor sign-in (issue #21): setting it up on the Me page, signing in with a code (a
// wrong one first), with a recovery code, and turning it off. The server is a small fake
// that checks real RFC 6238 codes; the test reads the key the page shows and works the
// codes out itself, the way an authenticator app would.

// base32Decode reads an RFC 4648 key (no padding).
function base32Decode(s: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0, value = 0;
  const out: number[] = [];
  for (const ch of s.replace(/\s+/g, "").toUpperCase()) {
    value = (value << 5) | alphabet.indexOf(ch);
    bits += 5;
    if (bits >= 8) { out.push((value >>> (bits - 8)) & 0xff); bits -= 8; }
  }
  return Buffer.from(out);
}

// totp is the six-digit code for one 30-second step.
function totp(secret: string, step: number): string {
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const sum = createHmac("sha1", base32Decode(secret)).update(msg).digest();
  const off = sum[sum.length - 1] & 0x0f;
  return String((sum.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

const ALPHABET = "abcdefghjkmnpqrstuvwxyz23456789";

// FakeTwoFactor is the server's side: one account, a virtual clock in 30-second steps, a
// code accepted ±1 step and once, recovery codes once each.
class FakeTwoFactor {
  secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP";
  enabled = false;
  step = 57_000_000;
  lastStep = 0;
  recovery = Array.from({ length: 10 }, (_, i) => `${ALPHABET.slice(i, i + 5)}-${ALPHABET.slice(i + 10, i + 15)}`);
  used = new Set<string>();

  check(code: string): boolean {
    const c = code.replace(/[\s-]/g, "").toLowerCase();
    if (/^\d{6}$/.test(c)) {
      for (let s = this.step - 1; s <= this.step + 1; s++) {
        if (s > this.lastStep && totp(this.secret, s) === c) { this.lastStep = s; return true; }
      }
      return false;
    }
    const hit = this.recovery.find((r) => r.replace("-", "") === c && !this.used.has(r));
    if (hit) { this.used.add(hit); return true; }
    return false;
  }
  /** What the app shows now. */
  appCode(): string {
    return totp(this.secret, this.step);
  }
  wrongCode(): string {
    const right = new Set([-1, 0, 1].map((d) => totp(this.secret, this.step + d)));
    return ["000000", "111111", "222222"].find((c) => !right.has(c))!;
  }
  state() {
    return { enabled: this.enabled, recovery_codes_left: this.recovery.length - this.used.size, password_set: true, required: false, ...(this.enabled ? { enabled_at: "2026-10-01T10:00:00Z" } : {}) };
  }
}

// override puts fixtures in front of the defaults (the last route registered is tried
// first), recording the calls like the defaults do.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method()) continue;
      const m = typeof r.path === "string" ? (url.pathname === r.path ? [] : null) : url.pathname.match(r.path);
      if (!m) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: m.slice(1), body }) : r.body;
      const failed = out && typeof out === "object" && "__status" in out ? (out as { __status: number }).__status : 0;
      return route.fulfill({ status: failed || r.status || 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

const refuse = (status: number, message: string, extra: object = {}) => ({ __status: status, status: "error", message, ...extra });

test.describe("Me page", () => {
  test.use({ persona: "admin" });

  test("sets two-factor up with the shown key, then turns it off with a code", async ({ page, api }) => {
    const fake = new FakeTwoFactor();
    await override(page, api, [
      { method: "GET", path: "/api/v1/me/2fa", respond: () => fake.state() },
      { method: "POST", path: "/api/v1/me/2fa/setup", respond: ({ body }) => (body as { password: string }).password === "correct horse"
        ? { secret: fake.secret, uri: `otpauth://totp/Arrmada:admiral?secret=${fake.secret}&issuer=Arrmada`, qr: { size: 21, path: "M0 0h7v7h-7z" } }
        : refuse(400, "Your password is wrong") },
      { method: "POST", path: "/api/v1/me/2fa/confirm", respond: ({ body }) => {
        if (!fake.check((body as { code: string }).code)) return refuse(400, "That code isn't right. Check the time on your phone and try the newest code.");
        fake.enabled = true;
        return { recovery_codes: fake.recovery };
      } },
      { method: "POST", path: "/api/v1/me/2fa/disable", respond: ({ body }) => {
        const b = body as { password: string; code: string };
        if (b.password !== "correct horse") return refuse(400, "Your password is wrong");
        if (!fake.check(b.code)) return refuse(400, "That code isn't right");
        fake.enabled = false;
        return { enabled: false };
      } },
    ]);
    await page.goto("/me");
    const card = page.locator("#two-factor");
    await expect(card.getByRole("heading", { name: /Two-factor sign-in/ })).toContainText("Off");
    await expect(card).toContainText("audiobook apps keep using your audiobook password");

    await card.getByRole("button", { name: "Set up" }).click();
    await card.getByLabel("Your password", { exact: true }).fill("wrong");
    await card.getByRole("button", { name: "Continue" }).click();
    await expect(card.getByRole("alert")).toHaveText("Your password is wrong");
    await card.getByLabel("Your password", { exact: true }).fill("correct horse");
    await card.getByRole("button", { name: "Continue" }).click();

    // The QR code and the key, grouped in fours; the test is the authenticator app.
    await expect(card.getByRole("img", { name: /QR code/ })).toBeVisible();
    const shown = card.getByTestId("totp-secret");
    await expect(shown).toHaveText("JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP");
    const secret = await shown.getAttribute("data-secret");
    expect(secret).toBe(fake.secret);

    await card.getByLabel("Code from the app").fill(fake.wrongCode());
    await card.getByRole("button", { name: "Turn on" }).click();
    await expect(card.getByRole("alert")).toContainText("That code isn't right");
    await card.getByLabel("Code from the app").fill(totp(secret!, fake.step));
    await card.getByRole("button", { name: "Turn on" }).click();

    // Recovery codes, shown once, with the offer to sign out other devices.
    await expect(card.getByRole("status")).toHaveText("Two-factor sign-in is on.");
    const codes = card.getByTestId("recovery-codes");
    await expect(codes.getByRole("listitem")).toHaveCount(10);
    await expect(codes).toContainText(fake.recovery[0]);
    await expect(card.getByRole("button", { name: "Download" })).toBeVisible();
    await expect(card.getByRole("button", { name: "Sign out other devices" })).toBeVisible();
    await card.getByRole("button", { name: "I’ve saved them" }).click();
    await expect(card.getByRole("heading", { name: /Two-factor sign-in/ })).toContainText("On");
    await expect(card).toContainText("10 of 10 recovery codes left");
    await expect(codes).toHaveCount(0);

    // Turning it off takes the password and a fresh code.
    fake.step++;
    await card.getByRole("button", { name: "Turn off" }).click();
    const form = card.getByRole("form", { name: "Turn off two-factor sign-in" });
    await form.getByLabel("Your password", { exact: true }).fill("correct horse");
    await form.getByLabel("Code from the app, or a recovery code").fill(fake.wrongCode());
    await form.getByRole("button", { name: "Turn off" }).click();
    await expect(form.getByRole("alert")).toHaveText("That code isn't right");
    await form.getByLabel("Code from the app, or a recovery code").fill(fake.appCode());
    await form.getByRole("button", { name: "Turn off" }).click();
    await expect(card.getByRole("heading", { name: /Two-factor sign-in/ })).toContainText("Off");
    await expect(card.getByRole("button", { name: "Set up" })).toBeVisible();

    expect(api.callsTo("POST", "/api/v1/me/2fa/disable").map((c) => (c.body as { password: string }).password)).toEqual(["correct horse", "correct horse"]);
  });
});

test.describe("sign-in page", () => {
  test.use({ persona: "requester" });

  // signedOut: no session, password sign-in answering a code ticket, and the fake checking
  // the codes at /auth/login/code.
  async function signedOut(page: Page, api: MockedApi, fake: FakeTwoFactor) {
    fake.enabled = true;
    let tickets = 0;
    await override(page, api, [
      { method: "GET", path: "/api/v1/auth/me", status: 401, body: { status: "error", message: "authentication required" } },
      { method: "GET", path: "/api/v1/status", body: { ...status(personas.requester), authenticated: false, plex_login: false } },
      { method: "POST", path: "/api/v1/auth/login", respond: ({ body }) => (body as { password: string }).password === "deck-password"
        ? { two_factor: "code", ticket: `ticket-${++tickets}` }
        : refuse(401, "invalid username or password") },
      { method: "POST", path: "/api/v1/auth/login/code", respond: ({ body }) => fake.check((body as { code: string }).code)
        ? { user: personas.requester.user }
        : refuse(401, "That code isn't right. Check the time on your phone, or use a recovery code.") },
    ]);
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.next", "/requests"); } });
  }

  async function password(page: Page) {
    await page.getByLabel("Email").fill("deckhand@example.com");
    await page.getByLabel("Password").fill("deck-password");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }

  test("asks for a code after the password; a wrong one is refused, the right one signs in", async ({ page, api }) => {
    const fake = new FakeTwoFactor();
    await signedOut(page, api, fake);
    await page.goto("/");
    await password(page);

    const step = page.getByRole("form", { name: "Two-factor sign-in" });
    await expect(step.getByRole("heading", { name: "Two-factor sign-in" })).toBeVisible();
    await step.getByLabel("Code", { exact: true }).fill(fake.wrongCode());
    await step.getByRole("button", { name: "Verify" }).click();
    await expect(step.getByRole("alert")).toContainText("That code isn't right");
    await expect(page).not.toHaveURL(/\/requests$/);

    await step.getByLabel("Code", { exact: true }).fill(fake.appCode());
    await step.getByRole("button", { name: "Verify" }).click();
    await expect(page).toHaveURL(/\/requests$/, { timeout: 10_000 });
    const tries = api.callsTo("POST", "/api/v1/auth/login/code").map((c) => c.body as { ticket: string; trust_device: boolean });
    expect(tries.map((t) => t.ticket)).toEqual(["ticket-1", "ticket-1"]);
    expect(tries.every((t) => t.trust_device === false)).toBe(true);
  });

  test("a recovery code signs in once, and can trust the device", async ({ page, api }) => {
    const fake = new FakeTwoFactor();
    await signedOut(page, api, fake);
    await page.goto("/");
    await password(page);
    const step = page.getByRole("form", { name: "Two-factor sign-in" });
    await step.getByRole("button", { name: "Use a recovery code" }).click();
    await step.getByLabel("Recovery code").fill(fake.recovery[3].toUpperCase());
    await step.getByLabel("Trust this device for 30 days").check();
    await step.getByRole("button", { name: "Verify" }).click();
    await expect(page).toHaveURL(/\/requests$/, { timeout: 10_000 });
    expect(api.callsTo("POST", "/api/v1/auth/login/code").at(-1)?.body).toEqual({ ticket: "ticket-1", code: fake.recovery[3].toUpperCase(), trust_device: true });

    // The same recovery code again: refused.
    await page.goto("/");
    await password(page);
    await step.getByRole("button", { name: "Use a recovery code" }).click();
    await step.getByLabel("Recovery code").fill(fake.recovery[3]);
    await step.getByRole("button", { name: "Verify" }).click();
    await expect(step.getByRole("alert")).toContainText("That code isn't right");
  });

  test("an expired ticket goes back to the password", async ({ page, api }) => {
    const fake = new FakeTwoFactor();
    await signedOut(page, api, fake);
    await override(page, api, [
      { method: "POST", path: "/api/v1/auth/login/code", respond: () => refuse(401, "that sign-in took too long — enter your password again", { reason: "ticket" }) },
    ]);
    await page.goto("/");
    await password(page);
    const step = page.getByRole("form", { name: "Two-factor sign-in" });
    await step.getByLabel("Code", { exact: true }).fill(fake.appCode());
    await step.getByRole("button", { name: "Verify" }).click();
    await expect(page.getByText("that sign-in took too long — enter your password again")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(step).toHaveCount(0);
  });
});

test.describe("People & access", () => {
  test.use({ persona: "admin" });

  test("shows who uses it, resets someone's, and can require it of admins", async ({ page, api }) => {
    let on = true;
    await override(page, api, [
      { method: "GET", path: "/api/v1/people", respond: () => ({ people: people.people.map((p) => (p.id === 5 ? { ...p, two_factor: on } : p)) }) },
      { method: "POST", path: "/api/v1/users/5/2fa/reset", respond: () => { on = false; return { reset: true }; } },
      { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) },
    ]);
    await page.goto("/settings/users");
    const grandad = page.getByTestId("person").filter({ hasText: "Grandad" });
    await expect(grandad.getByText("2FA", { exact: true })).toBeVisible();
    await grandad.getByRole("button", { name: "More…" }).click();
    await grandad.getByRole("button", { name: "Reset two-factor" }).click();
    const dialog = page.getByRole("dialog", { name: "Turn off Grandad's two-factor sign-in?" });
    await expect(dialog).toContainText("recorded in the log");
    await dialog.getByRole("button", { name: "Turn it off" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/users/5/2fa/reset").length).toBe(1);
    await expect(grandad.getByText("2FA", { exact: true })).toHaveCount(0);

    const policy = page.locator("#two-factor");
    await expect(policy).toContainText("audiobook apps keep their own audiobook password");
    await policy.getByRole("switch", { name: /Require two-factor for admins/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ two_factor_admins: true }]);
  });
});
