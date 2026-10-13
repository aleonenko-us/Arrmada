// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { LINKS } from "../../lib/links";
import { LibraryEmpty } from "./LibraryEmpty";

// COPY-14: an empty library says how to bring in what's already on disk (the page's own
// Scan library, and where the folder is set), not only "click Add".

afterEach(cleanup);

describe("LibraryEmpty", () => {
  it("offers the scan first, names the folder and links to where it is set", () => {
    const onScan = vi.fn();
    const onAdd = vi.fn();
    render(<MemoryRouter><LibraryEmpty noun="movies" folder="movies folder" addLabel="Add movie" onAdd={onAdd} onScan={onScan} /></MemoryRouter>);
    expect(screen.getByText("No movies yet")).toBeTruthy();
    expect(screen.getByText(/catalogs what's in your movies folder/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Settings → Library" }).getAttribute("href")).toBe(LINKS.libraryFolders);

    fireEvent.click(screen.getByRole("button", { name: "Scan library" }));
    expect(onScan).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "+ Add movie" }));
    expect(onAdd).toHaveBeenCalledOnce();
  });

  it("shows the scan as running and keeps Add off while it can't work", () => {
    render(<MemoryRouter><LibraryEmpty noun="books" folder="ebook and audiobook folders" addLabel="Add book" addDisabled onAdd={() => {}} onScan={() => {}} scanning /></MemoryRouter>);
    expect((screen.getByRole("button", { name: "Scanning…" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "+ Add book" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
