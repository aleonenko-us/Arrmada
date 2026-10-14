import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { Persona } from "./fixtures/users";
import { STAFF_PAGES, REQUESTER_PAGES, PAGE_EXTRA } from "./fixtures/pages";

// The colour-contrast guard: every staff page and Settings section, the requester's own
// pages and a few dialogs, in both themes, checked with axe's color-contrast rule (WCAG
// AA: 4.5:1 for body text, 3:1 for large text). It runs at 1440 and 375 px through the
// contrast-1440 / contrast-375 projects in playwright.config.ts.
//
// A failure lists each unreadable element with its colours and ratio. Fix it at the
// token (index.css) or by using a stronger one (ink-dim, not ink-faint, for words people
// need to read) — never by excluding the element here.

type Shot = { persona: Persona; path: string; open?: (page: Page) => Promise<void> };

const shots: Shot[] = [
  ...STAFF_PAGES.map((path) => ({ persona: "admin" as const, path })),
  ...REQUESTER_PAGES.map((path) => ({ persona: "requester" as const, path })),
  // Dialogs, sheets and menus: what opens on top of a page is drawn on other surfaces.
  { persona: "requester", path: "/discover/movie/1001" },
  { persona: "admin", path: "/requests?tab=active&type=movie&id=2" },
  {
    persona: "admin",
    path: "/movies/1",
    open: async (page) => {
      await page.getByRole("button", { name: /^more/i }).first().click();
    },
  },
  {
    persona: "admin",
    path: "/movies",
    open: async (page) => {
      await page.getByRole("button", { name: "+ Add movie" }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
    },
  },
  {
    persona: "admin",
    path: "/series",
    open: async (page) => {
      await page.getByRole("button", { name: /^Actions for / }).first().click();
      await page.getByRole("menuitem", { name: "Remove…" }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
    },
  },
  {
    persona: "requester",
    path: "/discover",
    open: async (page) => {
      await page.getByRole("button", { name: /account|deckhand/i }).first().click();
    },
  },
];

// The open list of a native <select> is drawn by the browser outside the page, so axe
// never sees it. Its options take their colours from CSS (index.css gives every option
// the panel and ink), so check what each option computes to instead.
async function optionContrast(page: Page) {
  return page.evaluate(() => {
    const rgb = (s: string) => (s.match(/[\d.]+/g) ?? []).map(Number);
    const lum = ([r, g, b]: number[]) => {
      const ch = (v: number) => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
      return 0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b);
    };
    const out: string[] = [];
    for (const o of Array.from(document.querySelectorAll("select option, select optgroup"))) {
      const cs = getComputedStyle(o);
      const fg = rgb(cs.color);
      const bg = rgb(cs.backgroundColor);
      if (bg.length === 4 && bg[3] === 0) {
        out.push(`${o.textContent?.trim()} — transparent background`);
        continue;
      }
      const [hi, lo] = [lum(fg), lum(bg)].sort((a, b) => b - a);
      const ratio = (hi + 0.05) / (lo + 0.05);
      const need = (o as HTMLOptionElement).disabled ? 3 : 4.5;
      if (ratio < need) out.push(`${o.textContent?.trim()} — ${cs.color} on ${cs.backgroundColor} = ${ratio.toFixed(2)}`);
    }
    return out;
  });
}

// Hover-only overlays (a card's quick Request) sit on a poster image that axe can't
// read; it reports those as "incomplete", not as violations, so nothing is skipped here.
async function audit(page: Page) {
  const res = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
  return res.violations.flatMap((v) =>
    v.nodes.map((n) => {
      const d = (n.any[0]?.data ?? {}) as { fgColor?: string; bgColor?: string; contrastRatio?: number; expectedContrastRatio?: string; fontSize?: string; fontWeight?: string };
      return `${n.target.join(" ")} — ${d.fgColor} on ${d.bgColor} = ${d.contrastRatio} (needs ${d.expectedContrastRatio}, ${d.fontSize} ${d.fontWeight}) — ${n.html.slice(0, 140)}`;
    }),
  );
}

for (const theme of ["dark", "light"] as const) {
  for (const { persona, path, open } of shots) {
    test.describe(`${theme} ${persona}`, () => {
      test.use({ persona });
      test(`${path}${open ? " (dialog)" : ""}`, async ({ page, api }) => {
        await page.addInitScript((t) => localStorage.setItem("arrmada.theme", t), theme);
        const extra = PAGE_EXTRA[path];
        if (extra) {
          await page.route((u) => u.pathname.startsWith("/api/"), (r) => {
            const key = `${r.request().method()} ${new URL(r.request().url()).pathname}`;
            return key in extra ? r.fulfill({ json: extra[key] }) : r.fallback();
          });
        }
        await page.goto(path);
        await expect(page.getByRole("heading").first()).toBeVisible();
        await api.quiet();
        if (open) {
          await open(page);
          await api.quiet();
        }
        // Entrance transitions would have axe measure a half-faded colour.
        await page.addStyleTag({ content: "*,*::before,*::after{transition:none!important;animation:none!important}" });
        expect.soft(await audit(page), "text below WCAG AA contrast").toEqual([]);
        expect.soft(await optionContrast(page), "select options below WCAG AA contrast").toEqual([]);
      });
    });
  }
}
