// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { api, type APIKeyStatus, type AppSettings, type PlexConfig, type ProwlarrInfo, type ProwlarrSyncResult, type SetupState } from "../lib/api";
import { indexerApi } from "../lib/indexerApi";
import { insightsApi } from "../lib/insightsApi";
import { plexApi } from "../lib/plexApi";
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
  // The Indexers step asks for Prowlarr on its own; by default there is none.
  vi.spyOn(setupApi, "stateWithProwlarr").mockResolvedValue({ ...state, indexers_enabled: 0, prowlarr_available: false });
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
    const field = await screen.findByLabelText("TMDB API key");
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
    const field = await screen.findByLabelText("TMDB API key");
    fireEvent.change(field, { target: { value: "wrong-key" } });
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    const dialog = await screen.findByRole("dialog", { name: "Save this TMDB key anyway?" });
    await act(async () => { fireEvent.click(within(dialog).getByText("Keep editing")); });
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
    const field = await screen.findByLabelText("TMDB API key");
    fireEvent.change(field, { target: { value: "good-key" } });
    await act(async () => { fireEvent.blur(field); });
    expect(await screen.findByText(/✓ OK: images from/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(save).toHaveBeenCalledWith("tmdb", "good-key");
    expect(test).toHaveBeenCalledTimes(1); // the blur and Next shared one check
  });
});

// The optional keys sit open under TMDB, Hardcover first and recommended; each typed one is
// checked before it's saved, and none of them is needed to carry on.
describe("SetupWizard optional keys", () => {
  const status = (id: string, over: Partial<APIKeyStatus> = {}): APIKeyStatus => ({
    id, label: id, purpose: "", help_url: "", steps: "", secret: id !== "opensubtitles_username", testable: true, tests_candidate: true,
    configured: false, source: "", env_set: false, ...over,
  });

  it("shows every optional key, Hardcover first and recommended, with a link to a free key", async () => {
    page();
    const section = await screen.findByRole("region", { name: "Optional keys — Arrmada works without them" });
    const names = within(section).getAllByText(/^(Hardcover|TheTVDB|OMDb|OpenSubtitles)$/).map((n) => n.textContent);
    expect(names).toEqual(["Hardcover", "TheTVDB", "OMDb", "OpenSubtitles"]);
    expect(within(section).getAllByText("Recommended")).toHaveLength(1);
    expect(within(section).getAllByText("Optional")).toHaveLength(3);
    expect(within(section).getByRole("link", { name: /Get a free Hardcover key/ }).getAttribute("href")).toBe("https://hardcover.app/account/api");
    expect(within(section).getByRole("link", { name: /Get a free OpenSubtitles key/ }).getAttribute("href")).toBe("https://www.opensubtitles.com/en/consumers");
    const os = within(section).getByRole("group", { name: /^OpenSubtitles/ });
    expect(within(os).getByLabelText("API key")).toBeTruthy();
    expect(within(os).getByLabelText("Username")).toBeTruthy();
    expect((within(os).getByLabelText("Password") as HTMLInputElement).type).toBe("password");
    expect(screen.getByText(/the key alone only searches/i)).toBeTruthy();
  });

  it("checks a pasted Hardcover key before saving and saves it once it passes", async () => {
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: true, detail: "OK: Hardcover accepted the key (signed in as reader)." });
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([]);
    page();
    const field = await screen.findByLabelText("Hardcover");
    await act(async () => {
      fireEvent.paste(field);
      fireEvent.change(field, { target: { value: "hc-token" } });
    });
    expect(test).toHaveBeenCalledWith("hardcover", "hc-token");
    expect(await screen.findByText(/✓ OK: Hardcover accepted the key/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(save).toHaveBeenCalledWith("hardcover", "hc-token");
    expect(test).toHaveBeenCalledTimes(1);
  });

  it("asks before saving an optional key its provider turned down", async () => {
    vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: false, detail: "OMDb: Invalid API key!" });
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([]);
    page();
    fireEvent.change(await screen.findByLabelText("OMDb"), { target: { value: "bad" } });
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    const dialog = await screen.findByRole("dialog", { name: "Save this OMDb key anyway?" });
    expect(within(dialog).getByText(/Optional keys can stay blank/)).toBeTruthy();
    await act(async () => { fireEvent.click(within(dialog).getByText("Keep editing")); });
    expect(save).not.toHaveBeenCalled();

    // Clearing the field carries on without it.
    fireEvent.change(screen.getByLabelText("OMDb"), { target: { value: "" } });
    await act(async () => { fireEvent.click(screen.getByText("Continue")); });
    expect(await screen.findByText("Where's your media?")).toBeTruthy();
    expect(save).not.toHaveBeenCalled();
  });

  it("saves a key that couldn't be checked without asking, and says so", async () => {
    vi.spyOn(api, "testAPIKey").mockRejectedValue(new Error("no test is available for that key"));
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([]);
    page();
    const field = await screen.findByLabelText("TheTVDB");
    fireEvent.change(field, { target: { value: "tvdb-key" } });
    await act(async () => { fireEvent.blur(field); });
    expect(await screen.findByText(/Couldn't check it \(no test is available for that key\)/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(save).toHaveBeenCalledWith("tvdb", "tvdb-key");
    expect(await screen.findByText("Where's your media?")).toBeTruthy();
  });

  it("checks the OpenSubtitles key with the login typed beside it, once focus leaves the group", async () => {
    const os = vi.spyOn(setupApi, "testOpenSubtitles").mockResolvedValue({ ok: true, detail: "OK: signed in as me, 20 downloads left today." });
    const save = vi.spyOn(api, "setAPIKey").mockResolvedValue([]);
    page();
    const group = await screen.findByRole("group", { name: /^OpenSubtitles/ });
    const key = within(group).getByLabelText("API key");
    const user = within(group).getByLabelText("Username");
    const pass = within(group).getByLabelText("Password");
    fireEvent.change(key, { target: { value: "os-key" } });
    fireEvent.blur(key, { relatedTarget: user });
    fireEvent.change(user, { target: { value: "me" } });
    fireEvent.change(pass, { target: { value: "secret" } });
    expect(os).not.toHaveBeenCalled(); // moving between its own fields isn't leaving it
    await act(async () => { fireEvent.blur(pass, { relatedTarget: null }); });
    expect(os).toHaveBeenCalledWith("os-key", "me", "secret");
    expect(await screen.findByText(/✓ OK: signed in as me/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Save and continue")); });
    expect(os).toHaveBeenCalledTimes(1);
    expect(save.mock.calls).toEqual([["opensubtitles_api", "os-key"], ["opensubtitles_username", "me"], ["opensubtitles_password", "secret"]]);
  });

  it("shows which keys are already set, masked, when setup runs again — and fills nothing in", async () => {
    vi.spyOn(api, "setupState").mockResolvedValue({
      ...state,
      keys: [tmdbKey, status("hardcover", { configured: true, source: "settings", hint: "…ab12" }), status("opensubtitles_username", { configured: true, source: "settings", hint: "me" })],
    });
    page();
    const hc = await screen.findByLabelText("Hardcover");
    expect((hc as HTMLInputElement).value).toBe("");
    expect(hc.getAttribute("placeholder")).toBe("Leave blank to keep the saved key");
    expect(screen.getByText("✓ saved (…ab12)")).toBeTruthy();
    expect(screen.getByText("✓ saved (me)")).toBeTruthy();
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
    await click("Continue"); // keys
    await click("Save and continue"); // folders
    await click("Skip this step"); // Plex
    await click("Skip this step"); // Indexers
    const todo = await screen.findByRole("link", { name: /Add an indexer \(to do\)/ });
    expect(todo.getAttribute("href")).toBe("/settings/connections#search");
    expect(screen.getByRole("link", { name: /Add your TMDB key \(done\)/ })).toBeTruthy();
    await act(async () => { fireEvent.click(todo); });
    expect(complete).toHaveBeenCalledOnce();
    expect(done).toHaveBeenCalledWith("/settings/connections#search");
  });
});

// Clicks the (first) element showing text once it's there, and lets the result settle.
async function click(text: string | RegExp) {
  const el = await screen.findByText(text);
  await act(async () => { fireEvent.click(el); });
}

// CFG-23: the Plex step reuses the Connections sign-in and saves the family switches as
// chosen — both off unless turned on here.
describe("SetupWizard Plex step", () => {
  const toPlex = async () => {
    vi.spyOn(api, "setLibraryPaths").mockResolvedValue(paths);
    page();
    await click("Continue");
    await click("Save and continue");
    expect(await screen.findByText("Connect Plex")).toBeTruthy();
  };
  beforeEach(() => {
    vi.spyOn(insightsApi, "insightsConfig").mockResolvedValue({ url: "", token_set: false, enabled: false, poll_seconds: 30, status: "unconfigured" } as unknown as PlexConfig);
    vi.spyOn(setupApi, "checklist").mockResolvedValue({ items: [], done: false, dismissed: false });
  });

  it("starts with family sign-in off and saves exactly what was chosen", async () => {
    const save = vi.spyOn(api, "updateSettings").mockResolvedValue({} as AppSettings);
    await toPlex();
    const allow = screen.getByRole("checkbox", { name: /Let family sign in with Plex/ }) as HTMLInputElement;
    expect(allow.checked).toBe(false);
    expect(screen.queryByRole("group", { name: "Auto-approve their requests" })).toBeNull();
    await act(async () => { fireEvent.click(allow); });
    const group = screen.getByRole("group", { name: "Auto-approve their requests" });
    for (const box of within(group).getAllByRole("checkbox")) expect((box as HTMLInputElement).checked).toBe(false);
    await act(async () => { fireEvent.click(within(group).getByRole("checkbox", { name: "Movies" })); });
    await click("Save and continue");
    expect(save).toHaveBeenCalledWith({ plex_login_enabled: true, plex_login_auto_approve_types: "movie" });
  });

  it("can be skipped without saving anything", async () => {
    const save = vi.spyOn(api, "updateSettings").mockResolvedValue({} as AppSettings);
    await toPlex();
    await click("Skip this step");
    expect(save).not.toHaveBeenCalled();
    expect(await screen.findByText("Where releases come from")).toBeTruthy();
  });

  it("explains when the server can't reach plex.tv, and still lets you go on", async () => {
    vi.spyOn(plexApi, "connectStart").mockRejectedValue(new Error("plex.tv: dial tcp: lookup plex.tv: no such host"));
    vi.spyOn(window, "open").mockReturnValue(null);
    await toPlex();
    await click("Sign in with Plex");
    expect(await screen.findByText(/Couldn't reach plex.tv from the server — you can connect later in Settings → Connections/)).toBeTruthy();
    expect(screen.getByText("Skip this step")).toBeTruthy();
  });
});

// CFG-24: the Indexers step offers a one-click Prowlarr sync when one answers, and
// Connections' add-indexer dialog for a single tracker.
describe("SetupWizard Indexers step", () => {
  const toIndexers = async () => {
    vi.spyOn(api, "setLibraryPaths").mockResolvedValue(paths);
    page();
    await click("Continue");
    await click("Save and continue");
    await click("Skip this step"); // Plex
    expect(await screen.findByText("Where releases come from")).toBeTruthy();
  };
  beforeEach(() => {
    vi.spyOn(insightsApi, "insightsConfig").mockResolvedValue({ url: "", token_set: false, enabled: false, poll_seconds: 30, status: "unconfigured" } as unknown as PlexConfig);
    vi.spyOn(indexerApi, "prowlarrInfo").mockResolvedValue({ url: "http://arrmada-prowlarr:9696", has_key: true, ui_port: "9696", ui_url: "" } as ProwlarrInfo);
  });

  it("syncs from a Prowlarr that answers, then shows what's enabled", async () => {
    const st = vi.spyOn(setupApi, "stateWithProwlarr").mockResolvedValue({ ...state, indexers_enabled: 0, prowlarr_available: true });
    const sync = vi.spyOn(indexerApi, "syncProwlarr").mockResolvedValue({ added: 3, updated: 0, unchanged: 0, disabled: 0, reenabled: 0, skipped_usenet: 0, flaresolverr_ready: true } as ProwlarrSyncResult);
    await toIndexers();
    expect(await screen.findByText(/Prowlarr is running — sync its indexers/)).toBeTruthy();
    st.mockResolvedValue({ ...state, indexers_enabled: 3, prowlarr_available: true });
    await click("Sync now");
    expect(sync).toHaveBeenCalledOnce();
    expect(await screen.findByText(/✓ 3 indexers enabled/)).toBeTruthy();
    // With indexers in place there's nothing to skip.
    expect(screen.queryByText("Skip this step")).toBeNull();
  });

  it("adds one tracker through the add-indexer dialog when there's no Prowlarr", async () => {
    vi.spyOn(setupApi, "stateWithProwlarr").mockResolvedValue({ ...state, indexers_enabled: 0, prowlarr_available: false });
    await toIndexers();
    expect(await screen.findByText(/No Prowlarr answered at its saved address/)).toBeTruthy();
    await click("Add a tracker…");
    expect(await screen.findByRole("dialog", { name: "Add an indexer" })).toBeTruthy();
  });
});
