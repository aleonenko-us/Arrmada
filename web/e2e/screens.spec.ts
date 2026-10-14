import { test, expect } from "./mockApi";
import type { Persona } from "./fixtures/users";
import { STAFF_PAGES as STAFF, REQUESTER_PAGES as REQUESTER, PAGE_EXTRA as EXTRA } from "./fixtures/pages";

// Before/after screenshots of every page (FE-18 widths, FE-30 type scale), run on
// demand and never in CI:
//
//   SCREENS_DIR=<folder outside the repo> npx playwright test --project=screens --update-snapshots   (before)
//   SCREENS_DIR=<same folder> npx playwright test --project=screens                                    (after)
//
// The first run writes the baseline; the second compares against it and fails each page
// that changed, with a diff image beside it. Snapshots live in SCREENS_DIR, so they are
// never committed. Each page is taken at 1440 and 375 px in both themes.
const shots: { persona: Persona; path: string }[] = [
  ...STAFF.map((path) => ({ persona: "admin" as const, path })),
  ...REQUESTER.map((path) => ({ persona: "requester" as const, path })),
];

for (const theme of ["dark", "light"] as const) {
  for (const { persona, path } of shots) {
    test.describe(`${theme} ${persona}`, () => {
      test.use({ persona });
      test(`${path}`, async ({ page, api }, info) => {
        await page.addInitScript((t) => localStorage.setItem("arrmada.theme", t), theme);
        const extra = EXTRA[path];
        if (extra) {
          await page.route((u) => u.pathname.startsWith("/api/"), (r) => {
            const key = `${r.request().method()} ${new URL(r.request().url()).pathname}`;
            return key in extra ? r.fulfill({ json: extra[key] }) : r.fallback();
          });
        }
        await page.goto(path);
        await expect(page.getByRole("heading").first()).toBeVisible();
        await api.quiet();
        // Fonts and lazy images settle before the picture is taken.
        await page.evaluate(() => document.fonts.ready);
        // FE-30's floor: no HTML text renders below 10.5px (the 2xs eyebrow size). Chart
        // labels inside an <svg> scale with the chart and are left out.
        const tiny = await page.evaluate(() => {
          const out: string[] = [];
          const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
          for (let n = walker.nextNode(); n; n = walker.nextNode()) {
            const el = n.parentElement;
            if (!el || !n.textContent?.trim() || el.closest("svg")) continue;
            const box = el.getBoundingClientRect();
            if (box.width === 0 || box.height === 0) continue;
            const px = parseFloat(getComputedStyle(el).fontSize);
            if (px < 10.5) out.push(`${px}px "${n.textContent.trim().slice(0, 30)}"`);
          }
          return out;
        });
        expect.soft(tiny, "text below the 10.5px floor").toEqual([]);
        const name = `${info.project.name}-${theme}-${persona}${path.replace(/[/?=#&]+/g, "_")}.png`;
        await expect.soft(page).toHaveScreenshot(name, { fullPage: true, animations: "disabled", caret: "hide", maxDiffPixels: 0 });
      });
    });
  }
}
