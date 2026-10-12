// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import SubtitleDrawer, { fmtCue, fmtShift } from "./SubtitleDrawer";
import { sidecarForLang, wantLine } from "./subtitles/WantChip";
import { subtitleFileApi, type SubFileView, type SubSidecar } from "../lib/subtitleFileApi";
import { MemoryRouter } from "react-router-dom";
import { ConfirmProvider, ToastProvider } from "../ui";

const en: SubSidecar = {
  name: "Heat (1995).en.srt", lang: "en", ext: ".srt", size: 2048, modified_at: 1, cue_count: 812,
  first_cues: [{ start_ms: 1000, end_ms: 2500, text: "Hello there." }, { start_ms: 3000, end_ms: 4000, text: "General Kenobi." }],
  shiftable: true, owned: true, source: "opensubtitles", provider: "opensubtitles", provider_file_id: "777",
  release: "Heat.1995.BluRay-GRP", score: 88, sync_confidence: 91,
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
  });
  it("finds the sidecar that covers a language, full before SDH", () => {
    const sdh = { ...en, name: "x.en.sdh.srt", variant: "sdh" as const };
    expect(sidecarForLang("en", [sdh, en], true)?.name).toBe(en.name);
    expect(sidecarForLang("en", [sdh], true)?.name).toBe("x.en.sdh.srt");
    expect(sidecarForLang("en", [{ ...en, lang: "" }], true)).toBeTruthy(); // an untagged one counts for the first language
    expect(sidecarForLang("es", [{ ...en, lang: "" }], false)).toBeUndefined();
  });
});
