// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { MyBook } from "../lib/api";
import { kindleApi } from "../lib/kindleApi";
import { KindleButton, kindleBlocked } from "./Kindle";

// BOOK-24: the first Send to Kindle explains Amazon's approved-senders list and asks for
// the Kindle's address before anything is sent; an AZW3 can't be sent and says why.

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const epub: MyBook = { book_id: 7, title: "Dune", audiobook: false, mine: false, ebook: { format: "epub", size_bytes: 1000, kindle: true } };

describe("KindleButton", () => {
  it("asks for the address first, then sends", async () => {
    vi.spyOn(kindleApi, "get").mockResolvedValue({ kindle_email: "", from_address: "books@example.com", smtp_configured: true });
    const set = vi.spyOn(kindleApi, "set").mockResolvedValue({ kindle_email: "reader_42@kindle.com", from_address: "books@example.com", smtp_configured: true });
    const send = vi.spyOn(kindleApi, "send").mockResolvedValue({ status: "sending" });
    render(<MemoryRouter><KindleButton book={epub} /></MemoryRouter>);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send to Kindle" })); });
    // The explainer names the address to approve; nothing is sent yet.
    expect(screen.getByText("books@example.com")).toBeTruthy();
    expect(screen.getByText(/Approved Personal Document E-mail List/)).toBeTruthy();
    expect(send).not.toHaveBeenCalled();

    fireEvent.change(screen.getByPlaceholderText("name_42@kindle.com"), { target: { value: "reader_42@kindle.com" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: /Save and send/ })); });
    expect(set).toHaveBeenCalledWith("reader_42@kindle.com");
    expect(send).toHaveBeenCalledWith(7);
  });

  it("is disabled, with the reason, for a format Kindle email doesn't take", () => {
    const azw3: MyBook = { ...epub, ebook: { format: "azw3", size_bytes: 1000, kindle: false } };
    render(<MemoryRouter><KindleButton book={azw3} /></MemoryRouter>);
    expect((screen.getByRole("button", { name: "Send to Kindle" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/AZW3 — Kindle email takes EPUB or PDF/)).toBeTruthy();
  });

  it("knows Amazon's size limit", () => {
    expect(kindleBlocked({ ...epub, ebook: { format: "pdf", size_bytes: 60 * 1024 * 1024, kindle: true } })).toMatch(/50 MB/);
    expect(kindleBlocked(epub)).toBe("");
  });
});
