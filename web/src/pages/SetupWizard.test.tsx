// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { api, type APIKeyStatus, type SetupState } from "../lib/api";
import { MemoryRouter } from "react-router-dom";
import { clearQueryCache } from "../lib/query";
import { setupApi } from "../lib/setupApi";
import { ConfirmProvider } from "../ui";
import { SetupWizard } from "./SetupWizard";

// INT-06: a wrong TMDB key shows ✗ in the wizard before it's saved, and Next asks first.

const paths = { movies: "", tv: "", ebooks: "", audiobooks: "", music: "", downloads: "" };
const tmdbKey: APIKeyStatus = { id: "tmdb", label: "TMDB", purpose: "", help_url: "", steps: "", secret: true, testable: true, tests_candidate: true, configured: false, source: "", env_set: false };
const state: SetupState = {
  needed: true, complete: false, tmdb_configured: false, libraries_chosen: false, keys: [tmdbKey],
  library: paths, running: paths, restart_needed: false, can_restart: false, mounts: [], suggestions: {},
};

beforeEach(() => {
  vi.spyOn(api, "setupState").mockResolvedValue(state);
  vi.spyOn(api, "checkLibraryFolder").mockReturnValue(new Promise(() => {}));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  clearQueryCache();
});

const page = (onDone: (to?: string) => void = () => {}) => render(<MemoryRouter><ConfirmProvider><SetupWizard onDone={onDone} /></ConfirmProvider></MemoryRouter>);

describe("SetupWizard TMDB key check", () => {
  it("checks a pasted key before saving and shows TMDB's answer", async () => {
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: false, detail: "TMDB rejected the key; use the v3 API key, not the v4 read access token" });
    page();
    const field = await screen.findByPlaceholderText("Paste the key");
    await act(async () => {
      fireEvent.paste(field);
      fireEvent.change(field, { target: { value: "wrong-key" } });
    });
    expect(test).toHaveBeenCalledWith("tmdb", "wrong-key");
    expect(await screen.findByText(/✗ TMDB rejected the key; use the v3 API key/)).toBeTruthy();
  });

  it("asks before saving a key that failed, and saves nothing on Cancel", async () => {
    vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: false, detail: "TMDB rejected the key" });
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([tmdbKey]);
    page();
    const field = await screen.findByPlaceholderText("Paste the key");
    fireEvent.change(field, { target: { value: "wrong-key" } });
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    const dialog = await screen.findByRole("dialog", { name: "Save this TMDB key anyway?" });
    await act(async () => { fireEvent.click(within(dialog).getByText("Cancel")); });
    expect(save).not.toHaveBeenCalled();
    expect(screen.getByText("Welcome to Arrmada")).toBeTruthy();

    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    await act(async () => { fireEvent.click(within(await screen.findByRole("dialog")).getByText("Save anyway")); });
    expect(save).toHaveBeenCalledWith("tmdb", "wrong-key");
  });

  it("saves a key that passed without asking", async () => {
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: true, detail: "OK: images from https://image.tmdb.org/t/p/" });
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([tmdbKey]);
    page();
    const field = await screen.findByPlaceholderText("Paste the key");
    fireEvent.change(field, { target: { value: "good-key" } });
    await act(async () => { fireEvent.blur(field); });
    expect(await screen.findByText(/✓ OK: images from/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(save).toHaveBeenCalledWith("tmdb", "good-key");
    expect(test).toHaveBeenCalledTimes(1); // the blur and Next shared one check
  });
});

// CFG-22: Skip only on the first step; Finish lists the Getting started steps as links.
describe("SetupWizard steps", () => {
  const checklist = {
    items: [
      { key: "tmdb", label: "Add your TMDB key", done: true, link: "/settings/connections#api-keys", link_key: "apiKeys" },
      { key: "indexer", label: "Add an indexer", done: false, link: "/settings/connections#search", link_key: "indexers" },
    ],
    done: false,
    dismissed: false,
  };

  it("offers Skip on the first step only", async () => {
    vi.spyOn(api, "setLibraryPaths").mockResolvedValue(paths);
    page();
    expect(await screen.findByText(/Skip for now — you can run it again from Settings → System/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Continue")); });
    expect(await screen.findByText("Where's your media?")).toBeTruthy();
    expect(screen.queryByText(/Skip/)).toBeNull();
    expect(screen.getByRole("button", { name: "Back" })).toBeTruthy();
  });

  it("skipping from the first step finishes setup", async () => {
    const complete = vi.spyOn(api, "completeSetup").mockResolvedValue({ status: "ok" });
    const done = vi.fn();
    page(done);
    const skip = await screen.findByText(/Skip for now/);
    await act(async () => { fireEvent.click(skip); });
    expect(complete).toHaveBeenCalledOnce();
    expect(done).toHaveBeenCalledWith(undefined);
  });

  it("ends on the live checklist, whose links finish setup and open the step", async () => {
    vi.spyOn(api, "setLibraryPaths").mockResolvedValue(paths);
    vi.spyOn(setupApi, "checklist").mockResolvedValue(checklist);
    const complete = vi.spyOn(api, "completeSetup").mockResolvedValue({ status: "ok" });
    const done = vi.fn();
    page(done);
    const next = await screen.findByText("Continue");
    await act(async () => { fireEvent.click(next); });
    const save = await screen.findByText("Save and continue");
    await act(async () => { fireEvent.click(save); });
    const todo = await screen.findByRole("link", { name: /Add an indexer \(to do\)/ });
    expect(todo.getAttribute("href")).toBe("/settings/connections#search");
    expect(screen.getByRole("link", { name: /Add your TMDB key \(done\)/ })).toBeTruthy();
    await act(async () => { fireEvent.click(todo); });
    expect(complete).toHaveBeenCalledOnce();
    expect(done).toHaveBeenCalledWith("/settings/connections#search");
  });
});
