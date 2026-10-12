// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import SubtitleDrawer, { fmtCue, fmtShift } from "./SubtitleDrawer";
import { sidecarForLang, wantLine } from "./subtitles/WantChip";
import { quotaLine } from "./subtitles/SearchPanel";
import { subtitleFileApi, type SubFileView, type SubSidecar } from "../lib/subtitleFileApi";
import { MemoryRouter } from "react-router-dom";
import { ConfirmProvider, ToastProvider } from "../ui";

const en: SubSidecar = {
  name: "Heat (1995).en.srt", lang: "en", ext: ".srt", size: 2048, modified_at: 1, cue_count: 812,
  first_cues: [{ start_ms: 1000, end_ms: 2500, text: "Hello there." }, { start_ms: 3000, end_ms: 4000, text: "General Kenobi." }],
  shiftable: true, owned: true, source: "opensubtitles", provider: "opensubtitles", provider_file_id: "777",
  release: "Heat.1995.BluRay-GRP", score: 88, health: { state: "ok", score: 91, note: "in sync · checked 12 Oct" },
};
const view: SubFileView = {
  kind: "movie", movie_id: 1, media_key: "m:1", title: "Heat", video_path: "/movies/Heat (1995)/Heat (1995).mkv",
  kept: ["en", "es"],
  languages: [{ lang: "en", have: true }, { lang: "es", have: false, reason: "no_match", next_retry_at: Math.floor(Date.now() / 1000) + 6 * 86400 }],
  audio: [{ index: 0, lang: "eng", default: true }],
  embedded: [{ index: 0, codec: "hdmv_pgs_subtitle", lang: "eng", text: false }],
  sidecars: [en],
  orphans: [],
  backoffs: [],
  attempts: [],
  jobs: [],
};

beforeEach(() => {
  vi.spyOn(subtitleFileApi, "view").mockResolvedValue(view);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mount() {
  return render(<MemoryRouter><ToastProvider><ConfirmProvider><SubtitleDrawer media={{ kind: "movie", movie_id: 1 }} onClose={() => {}} /></ConfirmProvider></ToastProvider></MemoryRouter>);
}

describe("SubtitleDrawer", () => {
  it("lists each sidecar with its source, Health and first lines", async () => {
    mount();
    expect(await screen.findByText("Hello there.")).toBeTruthy();
    expect(screen.getByText("Health 91")).toBeTruthy();
    expect(screen.getByText(/Heat\.1995\.BluRay-GRP · score 88/)).toBeTruthy();
    expect(screen.getByText("0:01.000")).toBeTruthy();
    expect(screen.getByText(/es missing · next try in 6 days/)).toBeTruthy();
  });

  it("nudges the preview, then applies the shift", async () => {
    const shift = vi.spyOn(subtitleFileApi, "shift").mockResolvedValue(en);
    mount();
    await screen.findByText("Hello there.");
    fireEvent.click(screen.getByRole("button", { name: "Move +1 s" }));
    fireEvent.click(screen.getByRole("button", { name: "Move +0.5 s" }));
    expect(screen.getByText("0:02.500")).toBeTruthy(); // the preview moves before Apply
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Apply +1.5 s" })); });
    expect(shift).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, "Heat (1995).en.srt", 1500);
  });

  it("asks before sending a file to the recycle bin", async () => {
    const remove = vi.spyOn(subtitleFileApi, "remove").mockResolvedValue({ removed: en.name });
    mount();
    await screen.findByText("Hello there.");
    fireEvent.click(screen.getByRole("button", { name: "Delete…" }));
    const dialog = await screen.findByRole("dialog", { name: /Delete Heat \(1995\)\.en\.srt\?/ });
    expect(dialog.textContent).toContain("recycle bin");
    expect(dialog.textContent).toContain("won't be fetched again");
    expect(remove).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(within(dialog).getByRole("button", { name: "Move to recycle bin" })); });
    expect(remove).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, "Heat (1995).en.srt");
  });
});

describe("subtitle words", () => {
  it("formats cue times and shifts", () => {
    expect(fmtCue(1000)).toBe("0:01.000");
    expect(fmtCue(3_723_456)).toBe("1:02:03.456");
    expect(fmtShift(1500)).toBe("+1.5 s");
    expect(fmtShift(-1000)).toBe("−1 s");
  });
  it("says what a kept language's state is", () => {
    expect(wantLine({ lang: "en", have: true }, en).text).toBe("✓ en · OpenSubtitles · Health 91");
    expect(wantLine({ lang: "fr", have: false, reason: "impossible", retry_on: "langs" }).text).toBe("fr missing · waiting for a language change");
    expect(wantLine({ lang: "de", have: false, source: "download", fallback: "ai" }).text).toBe("de missing · will try download → AI");
    expect(wantLine({ lang: "de", have: false }).text).toBe("de missing · no source");
  });
  it("finds the sidecar that covers a language, full before SDH", () => {
    const sdh = { ...en, name: "x.en.sdh.srt", variant: "sdh" as const };
    expect(sidecarForLang("en", [sdh, en], true)?.name).toBe(en.name);
    expect(sidecarForLang("en", [sdh], true)?.name).toBe("x.en.sdh.srt");
    expect(sidecarForLang("en", [{ ...en, lang: "" }], true)).toBeTruthy(); // an untagged one counts for the first language
    expect(sidecarForLang("es", [{ ...en, lang: "" }], false)).toBeUndefined();
  });
});

describe("SubtitleDrawer actions (SUB-27/28)", () => {
  const ok = async (name: RegExp) => {
    const dialog = await screen.findByRole("dialog", { name });
    await act(async () => { fireEvent.click(within(dialog).getByRole("button", { name: "Queue it" })); });
  };

  it("Generate with AI queues an AI redo of that language", async () => {
    const redo = vi.spyOn(subtitleFileApi, "redo").mockResolvedValue({ id: 5 } as never);
    mount();
    await screen.findByText("Hello there.");
    fireEvent.click(screen.getAllByRole("button", { name: "Generate with AI" })[1]); // es
    await ok(/^Generate .* with AI\?$/);
    expect(redo).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, { lang: "es", mode: "ai" });
  });

  it("Extract queues the chosen embedded track as its language", async () => {
    vi.spyOn(subtitleFileApi, "view").mockResolvedValue({
      ...view,
      embedded: [{ index: 2, codec: "subrip", lang: "spa", text: true, sdh: true }, { index: 3, codec: "subrip", lang: "eng", text: true, forced: true }],
    });
    const redo = vi.spyOn(subtitleFileApi, "redo").mockResolvedValue({ id: 6 } as never);
    mount();
    await screen.findByText("Hello there.");
    expect(screen.getAllByRole("button", { name: "Extract" })).toHaveLength(1); // never the forced track
    fireEvent.click(screen.getByRole("button", { name: "Extract" }));
    await ok(/Extract track #2/);
    expect(redo).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, { lang: "spa", variant: "sdh", mode: "extract", track_index: 2 });
  });

  it("searches OpenSubtitles, shows scores and picks one", async () => {
    const search = vi.spyOn(subtitleFileApi, "search").mockResolvedValue({
      lang: "en", min_score: 60, can_download: true, quota_remaining: 18, quota_reset_at: 0, provider_state: "ok", searched_at: 1,
      current_file_id: "777",
      candidates: [
        { file_id: "1", language: "en", release: "Heat.1995.1080p.BluRay-GRP", downloads: 40, hearing_impaired: false, hash_match: true, score: 100, reasons: ["+100 hash match"] },
        { file_id: "777", language: "en", release: "Heat.1995.BluRay-GRP", downloads: 900, hearing_impaired: true, hash_match: false, score: 70, reasons: ["+20 same source (BluRay)"] },
        { file_id: "9", language: "en", release: "Heat.1995.WEB", downloads: 5, hearing_impaired: false, hash_match: false, score: 10, reasons: [], blocklisted: true },
      ],
    });
    const redo = vi.spyOn(subtitleFileApi, "redo").mockResolvedValue({ id: 7 } as never);
    mount();
    await screen.findByText("Hello there.");
    fireEvent.click(screen.getAllByRole("button", { name: "Search OpenSubtitles" })[0]);
    const panel = await screen.findByRole("region", { name: /OpenSubtitles results for/ });
    expect(await within(panel).findByText("Heat.1995.1080p.BluRay-GRP")).toBeTruthy();
    expect(search).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, "en", false);
    expect(within(panel).getByText("18 downloads left today. Searching is free.")).toBeTruthy();
    expect(within(panel).getByText("100").getAttribute("title")).toContain("+100 hash match");
    const uses = within(panel).getAllByRole("button", { name: "Use this" });
    expect(uses.map((b) => (b as HTMLButtonElement).disabled)).toEqual([false, true, true]); // in use; blocked
    fireEvent.click(uses[0]);
    const dialog = await screen.findByRole("dialog", { name: /Use Heat\.1995\.1080p\.BluRay-GRP\?/ });
    await act(async () => { fireEvent.click(within(dialog).getByRole("button", { name: "Use this" })); });
    expect(redo).toHaveBeenCalledWith({ kind: "movie", movie_id: 1 }, { lang: "en", mode: "pick", file_id: "1" });
  });

  it("says when a spent quota resets, and that it blocks Use this", () => {
    const base = { lang: "en", min_score: 60, candidates: [], provider_state: "quota_spent" as const, searched_at: 1, quota_remaining: 0 };
    const spent = quotaLine({ ...base, can_download: true, quota_reset_at: 2_000_000_000 }, 1_900_000_000_000);
    expect(spent.spent).toBe(true);
    expect(spent.text).toMatch(/used up — resets/);
    expect(quotaLine({ ...base, can_download: false, quota_reset_at: 0 }).spent).toBe(true);
    expect(quotaLine({ ...base, can_download: true, quota_reset_at: 0, quota_remaining: 1 }).text).toBe("1 download left today. Searching is free.");
  });
});
