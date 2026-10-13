// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { clearQueryCache } from "../lib/query";
import { setupApi, type SetupChecklist } from "../lib/setupApi";
import { ToastProvider } from "../ui";
import { GettingStarted } from "./GettingStarted";

const list = (over: Partial<SetupChecklist> = {}): SetupChecklist => ({
  items: [
    { key: "tmdb", label: "Add your TMDB key", done: true, link: "/settings/connections#api-keys", link_key: "apiKeys" },
    { key: "indexer", label: "Add an indexer", done: false, link: "/settings/connections#search", link_key: "indexers" },
  ],
  done: false,
  dismissed: false,
  ...over,
});

const page = () => render(<MemoryRouter><ToastProvider><GettingStarted /></ToastProvider></MemoryRouter>);

afterEach(() => {
  cleanup();
  clearQueryCache();
  vi.restoreAllMocks();
});

describe("Getting started card", () => {
  it("lists each step ticked or not, linking to where it's done", async () => {
    vi.spyOn(setupApi, "checklist").mockResolvedValue(list());
    page();
    const todo = await screen.findByRole("link", { name: /Add an indexer \(to do\)/ });
    expect(todo.getAttribute("href")).toBe("/settings/connections#search");
    expect(screen.getByRole("link", { name: /Add your TMDB key \(done\)/ })).toBeTruthy();
    expect(screen.getByText(/1 to go/)).toBeTruthy();
  });

  it("stays gone once everything is done", async () => {
    const get = vi.spyOn(setupApi, "checklist").mockResolvedValue(list({ done: true }));
    const { container } = page();
    await act(async () => { await get.mock.results[0]?.value; });
    expect(container.textContent).toBe("");
  });

  it("Dismiss hides it for good", async () => {
    vi.spyOn(setupApi, "checklist").mockResolvedValue(list());
    const dismiss = vi.spyOn(setupApi, "dismissChecklist").mockResolvedValue(list({ dismissed: true }));
    const { container } = page();
    fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    await act(async () => { await dismiss.mock.results[0]?.value; });
    expect(dismiss).toHaveBeenCalledOnce();
    expect(container.textContent).toBe("");
  });
});
