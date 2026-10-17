// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import CommandPalette from "./CommandPalette";
import { api, type AuthUser } from "../../lib/api";
import { searchApi } from "../../lib/searchApi";
import { ToastProvider } from "../../ui";

// The palette's keyboard: typing filters, the arrows move the highlight, Enter opens the
// highlighted row (closing the palette first), Escape closes; a requester never asks for
// library titles.

const me = { user: null as AuthUser | null, external: false, booksEnabled: true, musicEnabled: false, metadataReady: true };
vi.mock("../../lib/me", async (orig) => ({ ...(await orig<typeof import("../../lib/me")>()), useMe: () => me }));

const at = "2026-10-01 09:00:00";
const user = (role: AuthUser["role"]): AuthUser => ({ id: 3, username: "pat", role, auto_approve: false, created_at: at });

function Where() {
  const loc = useLocation();
  return <output data-testid="where">{loc.pathname}{loc.search}{loc.hash}</output>;
}

function Harness() {
  const [open, setOpen] = useState(true);
  return (
    <>
      <Where />
      {open ? <CommandPalette onClose={() => setOpen(false)} /> : <span>closed</span>}
    </>
  );
}

function mount() {
  render(
    <MemoryRouter initialEntries={["/activity/queue"]}>
      <ToastProvider>
        <Harness />
      </ToastProvider>
    </MemoryRouter>,
  );
  return screen.getByRole("combobox");
}

const where = () => screen.getByTestId("where").textContent;
const activeLabel = () => {
  const input = screen.getByRole("combobox");
  const id = input.getAttribute("aria-activedescendant");
  return id ? document.getElementById(id)?.textContent ?? "" : "";
};

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(api, "discoverSearch").mockResolvedValue({ items: [] });
  // jsdom has no layout: scrolling the highlighted row into view is a no-op.
  Element.prototype.scrollIntoView = () => {};
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  document.body.style.overflow = "";
});

describe("CommandPalette", () => {
  it("moves with the arrows and opens the highlighted row with Enter", async () => {
    me.user = user("admin");
    vi.spyOn(searchApi, "library").mockResolvedValue([]);
    const input = mount();
    expect(document.activeElement).toBe(input);
    fireEvent.change(input, { target: { value: "naming" } });
    expect(activeLabel()).toContain("Book naming");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    const second = activeLabel();
    expect(second).not.toContain("Book naming");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    fireEvent.keyDown(input, { key: "ArrowUp" }); // wraps to the last row
    fireEvent.keyDown(input, { key: "ArrowDown" }); // and back to the first
    expect(activeLabel()).toContain("Book naming");
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(screen.getByText("closed")).toBeTruthy());
    expect(where()).toBe("/settings/media#book-naming");
  });

  it("lists library titles for staff and opens one", async () => {
    me.user = user("manager");
    const lib = vi.spyOn(searchApi, "library").mockResolvedValue([{ kind: "movie", id: 7, title: "Dune", year: 2021 }]);
    const input = mount();
    fireEvent.change(input, { target: { value: "dune" } });
    await waitFor(() => expect(screen.getByRole("group", { name: "Library" })).toBeTruthy());
    expect(lib).toHaveBeenCalledWith("dune");
    expect(activeLabel()).toContain("Dune");
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(where()).toBe("/movies/7"));
  });

  it("never asks a requester's palette for library titles", async () => {
    me.user = user("requester");
    const lib = vi.spyOn(searchApi, "library").mockResolvedValue([{ kind: "movie", id: 7, title: "Dune" }]);
    const input = mount();
    fireEvent.change(input, { target: { value: "dune" } });
    await waitFor(() => expect(api.discoverSearch).toHaveBeenCalledWith("dune"));
    expect(lib).not.toHaveBeenCalled();
    expect(screen.queryByRole("group", { name: "Library" })).toBeNull();
    // (jsdom names the row with spaces around the highlighted word.)
    expect(screen.getByRole("option", { name: /Search Discover for “\s*dune\s*”/ })).toBeTruthy();
  });

  it("closes on Escape and stays where it was", async () => {
    me.user = user("admin");
    const input = mount();
    await act(async () => { await new Promise((r) => setTimeout(r, 10)); });
    fireEvent.keyDown(input, { key: "Escape" });
    await waitFor(() => expect(screen.getByText("closed")).toBeTruthy());
    expect(where()).toBe("/activity/queue");
  });

  it("remembers what was opened and lists it first next time", async () => {
    me.user = user("admin");
    let input = mount();
    fireEvent.change(input, { target: { value: "calendar" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(where()).toBe("/calendar"));
    cleanup();
    input = mount();
    expect(screen.getByRole("group", { name: "Recent" }).textContent).toContain("Calendar");
    expect(input.getAttribute("aria-activedescendant")).toBeTruthy();
  });
});
