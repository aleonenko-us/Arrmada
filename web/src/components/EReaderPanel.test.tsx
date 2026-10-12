// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { EReaderPanel, opdsAddress, READER_APPS } from "./EReaderPanel";

// BOOK-22: "Read on your e-reader" — the OPDS address, the sign-in, and the reader apps.

afterEach(cleanup);

const show = (password: { has: boolean; allowed: boolean } | null) =>
  render(<MemoryRouter><EReaderPanel password={password} /></MemoryRouter>);

describe("opdsAddress", () => {
  it("is /opds on the page's own origin", () => {
    expect(opdsAddress("https://books.example")).toBe("https://books.example/opds");
    expect(opdsAddress("http://192.168.1.5:8080/")).toBe("http://192.168.1.5:8080/opds");
  });
});

describe("EReaderPanel", () => {
  it("shows the address, the password to use and every reader app", () => {
    show({ has: true, allowed: true });
    expect(screen.getByText(opdsAddress(window.location.origin))).toBeTruthy();
    expect(screen.getByText(/audiobook & e-reader password/)).toBeTruthy();
    expect(screen.getByText(/not your Arrmada password/)).toBeTruthy();
    for (const a of READER_APPS) expect(screen.getByText(a.name)).toBeTruthy();
    expect(screen.getByText(/Apple Books can't open OPDS/)).toBeTruthy();
  });

  it("points to where the password is set when there isn't one", () => {
    show({ has: false, allowed: true });
    const link = screen.getByRole("link", { name: /set it in Audiobooks/ });
    expect(link.getAttribute("href")).toBe("/audiobooks?tab=apps");
  });

  it("says so when the account may not connect", () => {
    show({ has: true, allowed: false });
    expect(screen.getByText(/isn't set up for reading apps/)).toBeTruthy();
    expect(screen.queryByText(opdsAddress(window.location.origin))).toBeNull();
  });
});
