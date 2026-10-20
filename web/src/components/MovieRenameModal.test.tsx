// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { api, type MovieRenameItem } from "../lib/api";
import { MovieRenameModal } from "./MovieRenameModal";

// The rename preview lists everything that moves with a file (its subtitles, the movie's
// own folder's artwork) and why the old folder's other files stay; Apply sends the items
// back with that list, so the server moves no more than the preview showed.

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const item = (extra: Partial<MovieRenameItem>): MovieRenameItem => ({
  media: "movie", movie_id: 7, version_id: 0, title: "Heat", year: 1995, label: "Default",
  current: "/m/heat.1995.old/heat.1995.mkv", proposed: "/m/Heat (1995)/Heat (1995).mkv", status: "ok", ...extra,
});

describe("Movie rename preview", () => {
  it("lists what moves with the file and sends it back on apply", async () => {
    const it0 = item({ extras: [
      { kind: "subtitle", from: "/m/heat.1995.old/heat.1995.en.srt", to: "/m/Heat (1995)/Heat (1995).en.srt" },
      { kind: "folder", from: "/m/heat.1995.old/poster.jpg", to: "/m/Heat (1995)/poster.jpg" },
    ] });
    vi.spyOn(api, "renamePreview").mockResolvedValue({ items: [it0], matches: false });
    const rename = vi.spyOn(api, "renameMovie").mockResolvedValue({ renamed: 1, skipped: 0, results: [] });
    render(<MovieRenameModal movieId={7} title="Heat" onClose={() => {}} onRenamed={() => {}} />);
    expect(await screen.findByText("+ heat.1995.old/poster.jpg → Heat (1995)/poster.jpg")).toBeTruthy();
    expect(screen.getByText("+ heat.1995.old/heat.1995.en.srt → Heat (1995)/Heat (1995).en.srt")).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Rename 1 file" })); });
    expect(rename).toHaveBeenCalledWith(7, [it0]);
  });

  it("says why the old folder's other files stay", async () => {
    vi.spyOn(api, "renamePreview").mockResolvedValue({ items: [item({ kept: "its name isn't the movie's" })], matches: false });
    render(<MovieRenameModal movieId={7} title="Heat" onClose={() => {}} onRenamed={() => {}} />);
    expect(await screen.findByText("Other files in the old folder stay: its name isn't the movie's")).toBeTruthy();
  });
});
