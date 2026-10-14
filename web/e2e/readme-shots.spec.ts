import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { Persona } from "./fixtures/users";
import { PAGE_EXTRA as EXTRA } from "./fixtures/pages";
import { setup as configuredSetup } from "./fixtures/system";

// The pictures in README.md and on the project website (docs/images/*.png), taken from
// the mocked fixtures — never a real server or anyone's real library. Run on demand only:
//
//   README_SHOTS=1 npx playwright test --project=readme-shots
//
// Each test writes one file. Dark theme, 1440 px wide; the phone shots are 390 px.

const OUT = "../docs/images";

// Posters, backdrops and covers point at TMDB / Open Library. The other specs answer them
// with a blank pixel; here each gets a made-up poster (a gradient landscape picked from the
// URL), so the grids look like a library without showing anyone's real artwork.
const PALETTES = [
  ["#1d2b4a", "#c8553d", "#f2d0a4"],
  ["#14342b", "#3d8361", "#d6cda4"],
  ["#2b193d", "#7b3e6e", "#f4a259"],
  ["#0f2d3d", "#2e86ab", "#f6f5ae"],
  ["#3a1f1d", "#a4442b", "#e9c46a"],
  ["#1b1b2f", "#4f4f8f", "#e2c2c6"],
  ["#22333b", "#5e7f73", "#eae0d5"],
  ["#2d1e2f", "#b5446e", "#f7c59f"],
  ["#102a43", "#829ab1", "#f0b429"],
  ["#283618", "#606c38", "#fefae0"],
];

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}

function art(url: string): string {
  const h = hash(url);
  const [deep, mid, light] = PALETTES[h % PALETTES.length];
  const wide = /backdrop|original|w1280|coll/.test(url);
  const [w, ht] = wide ? [1280, 720] : [500, 750];
  const sunX = 0.25 * w + ((h >> 4) % 50) / 100 * w;
  const sunY = ht * (0.25 + ((h >> 9) % 20) / 100);
  const ridge = (base: number, amp: number, seed: number) => {
    const pts: string[] = [`0,${ht}`];
    for (let i = 0; i <= 8; i++) {
      const x = (i / 8) * w;
      const y = ht * base - Math.sin(i * 1.3 + seed) * amp * ht - ((seed * 37 + i * 11) % 7) * ht * 0.006;
      pts.push(`${x.toFixed(0)},${y.toFixed(0)}`);
    }
    pts.push(`${w},${ht}`);
    return pts.join(" ");
  };
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${ht}" viewBox="0 0 ${w} ${ht}">
  <defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${deep}"/><stop offset="0.75" stop-color="${mid}"/><stop offset="1" stop-color="${light}"/></linearGradient></defs>
  <rect width="${w}" height="${ht}" fill="url(#g)"/>
  <circle cx="${sunX.toFixed(0)}" cy="${sunY.toFixed(0)}" r="${(Math.min(w, ht) * 0.13).toFixed(0)}" fill="${light}" opacity="0.85"/>
  <polygon points="${ridge(0.7, 0.06, h % 5)}" fill="${deep}" opacity="0.55"/>
  <polygon points="${ridge(0.82, 0.04, (h >> 3) % 7)}" fill="${deep}" opacity="0.8"/>
  <polygon points="${ridge(0.92, 0.025, (h >> 6) % 11)}" fill="#0b0b10" opacity="0.6"/>
</svg>`;
}

async function posters(page: Page) {
  await page.route(/^https?:\/\/(?!localhost[:/])/, (route: Route) =>
    route.fulfill({ status: 200, contentType: "image/svg+xml", body: art(route.request().url()) }),
  );
}

// extra answers the endpoints a page needs beyond the shared fixtures.
async function extra(page: Page, answers: Record<string, unknown> | undefined) {
  if (!answers) return;
  await page.route((u) => u.pathname.startsWith("/api/"), (r) => {
    const key = `${r.request().method()} ${new URL(r.request().url()).pathname}`;
    return key in answers ? r.fulfill({ json: answers[key] }) : r.fallback();
  });
}

async function settle(page: Page, api: { quiet: () => Promise<void> }) {
  await expect(page.getByRole("heading").first()).toBeVisible();
  await api.quiet();
  await page.evaluate(() => document.fonts.ready);
  // Let the poster images decode before the picture is taken.
  // (Lazy images below the fold never load, so only the ones on screen are waited for.)
  await page.waitForFunction(() => Array.from(document.images).every((i) => {
    const r = i.getBoundingClientRect();
    return i.complete || r.bottom < 0 || r.top > window.innerHeight;
  }));
  await page.waitForTimeout(300);
}

const DESKTOP: { file: string; path: string; persona?: Persona }[] = [
  { file: "discover", path: "/discover" },
  { file: "movies", path: "/movies" },
  { file: "series-detail", path: "/series/1" },
  { file: "activity", path: "/activity/queue" },
  { file: "requests", path: "/requests" },
  { file: "quality-profile", path: "/quality/movie/hd-1080p" },
];

const PHONE: { file: string; path: string; persona: Persona }[] = [
  { file: "phone-discover", path: "/discover", persona: "requester" },
  { file: "phone-requests", path: "/requests", persona: "requester" },
];

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("arrmada.theme", "dark"));
  await page.emulateMedia({ colorScheme: "dark" });
  await posters(page);
});

test.describe("desktop", () => {
  test.skip(({ viewport }) => (viewport?.width ?? 0) < 1000, "desktop shots");
  for (const s of DESKTOP) {
    test(s.file, async ({ page, api }) => {
      await extra(page, EXTRA[s.path]);
      await page.goto(s.path);
      await settle(page, api);
      await page.screenshot({ path: `${OUT}/${s.file}.png`, animations: "disabled", caret: "hide" });
    });
  }

  // The first thing a new install shows: the setup wizard's first step.
  test("setup-wizard", async ({ page, api }) => {
    const blank = { movies: "", tv: "", ebooks: "", audiobooks: "", music: "", downloads: "" };
    await extra(page, {
      "GET /api/v1/setup": {
        ...configuredSetup, needed: true, complete: false, tmdb_configured: false, libraries_chosen: false,
        library: blank, running: blank, suggestions: {},
      },
    });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome to Arrmada" })).toBeVisible();
    await settle(page, api);
    await page.screenshot({ path: `${OUT}/setup-wizard.png`, animations: "disabled", caret: "hide" });
  });
});

test.describe("phone", () => {
  test.skip(({ viewport }) => (viewport?.width ?? 0) >= 1000, "phone shots");
  for (const s of PHONE) {
    test.describe(s.file, () => {
      test.use({ persona: s.persona });
      test(s.file, async ({ page, api }) => {
        await page.goto(s.path);
        await settle(page, api);
        await page.screenshot({ path: `${OUT}/${s.file}.png`, animations: "disabled", caret: "hide" });
      });
    });
  }
});
