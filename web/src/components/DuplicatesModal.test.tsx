// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ConfirmProvider, ToastProvider } from "../ui";
import { bookDupesApi, type DupGroup, type PastMerge } from "../lib/bookDupesApi";
import DuplicatesModal from "./DuplicatesModal";

// BOOK-12: the possible-duplicates review merges into the chosen book after a confirm,
// "Not the same" hides a pair (and it stays gone on reload), left-over files are listed,
// and an earlier automatic merge links to Discover to add the lost book back.

const dune = (id: number, key: string, extra: Partial<DupGroup["books"][number]> = {}) => ({
  id, title: "Dune", author: "Frank Herbert", key, source: key.startsWith("hc:") ? "hardcover" : "openlibrary",
  audio_versions: 0, monitored: true, ...extra,
});
const group: DupGroup = {
  books: [dune(1, "OL1W", { ebook: { path: "/lib/e/1.epub", format: "EPUB", size_bytes: 1000, file_count: 1 } }), dune(2, "hc:2")],
  suggested_keep_id: 1,
  reasons: ["Same title and author"],
};
const past: PastMerge[] = [{
  book_id: 5, keeper_title: "Mistborn: The Final Empire", detail: "Merged duplicate entry \"Mistborn: Secret History\" (OL9W)",
  created_at: "2026-09-01 10:00:00", automatic: true, lost_title: "Mistborn: Secret History", lost_key: "OL9W",
}];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const open = (onChanged = () => {}) => render(
  <MemoryRouter>
    <ToastProvider><ConfirmProvider><DuplicatesModal onClose={() => {}} onChanged={onChanged} /></ConfirmProvider></ToastProvider>
  </MemoryRouter>,
);

describe("Possible duplicates", () => {
  it("Not the same hides the pair, and it stays gone on reload", async () => {
    const list = vi.spyOn(bookDupesApi, "list").mockResolvedValue({ groups: [group], past_merges: [] });
    const ignore = vi.spyOn(bookDupesApi, "ignore").mockResolvedValue(undefined);
    const changed = vi.fn();
    const first = open(changed);
    expect((await screen.findAllByText("Dune")).length).toBe(2);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Not the same" })); });
    expect(ignore).toHaveBeenCalledWith([1, 2]);
    expect(changed).toHaveBeenCalled();
    expect(screen.queryByText("Dune")).toBeNull();
    first.unmount();

    // The server no longer lists it.
    list.mockResolvedValue({ groups: [], past_merges: [] });
    open();
    expect(await screen.findByText("No possible duplicates")).toBeTruthy();
  });

  it("merges into the chosen book after a confirm and lists files left on disk", async () => {
    vi.spyOn(bookDupesApi, "list").mockResolvedValue({ groups: [group], past_merges: [] });
    const merge = vi.spyOn(bookDupesApi, "merge").mockResolvedValue({
      keep_id: 2, left_files: ["/lib/e/1.epub"],
      results: [{ drop_id: 1, drop_title: "Dune", moved_ebook: false, moved_audiobook: false, versions: 0, left_files: ["/lib/e/1.epub"] }],
    });
    open();
    // The suggestion is preselected; keep the other one instead.
    const keep2 = await screen.findByLabelText("Keep “Dune”", { selector: "input[type=radio]:not(:checked)" });
    await act(async () => { fireEvent.click(keep2); });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Merge into the kept book" })); });
    expect(merge).not.toHaveBeenCalled(); // nothing merges before the confirm
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Merge" })); });
    expect(merge).toHaveBeenCalledWith(2, [1]);
    expect(await screen.findByText("/lib/e/1.epub")).toBeTruthy();
  });

  it("lists earlier automatic merges with a way back from Discover", async () => {
    vi.spyOn(bookDupesApi, "list").mockResolvedValue({ groups: [], past_merges: past });
    open();
    const link = await screen.findByRole("link", { name: "Find on Discover" });
    expect(link.getAttribute("href")).toBe("/discover?tab=books&q=Mistborn%3A%20Secret%20History");
    expect(screen.getByText("Automatic")).toBeTruthy();
  });
});
