// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api, type APIKeyStatus, type AppSettings } from "../../lib/api";
import { connectionsApi, type ConnectionItem } from "../../lib/connectionsApi";
import { clearQueryCache } from "../../lib/query";
import { ConfirmProvider } from "../../ui";
import { ConnectionsSettings } from "./Connections";

// INT-06 / CFG-15: the API keys are edited inside their cards on Settings → Connections.
// Every testable key has a Test, a typed value can be tested before it's saved, a key is
// tested straight after it's saved, clearing asks first, and a blank Save never reaches
// the server.

vi.mock("../../lib/me", async (orig) => ({
  ...(await orig<typeof import("../../lib/me")>()),
  useMe: () => ({ user: { id: 1, username: "admin", role: "admin" }, setMetadataReady: () => {} }),
}));
// The Discovery region card saves through the shared settings store; these tests are about
// the keys, so it gets a still store.
vi.mock("../../lib/useSettings", () => ({
  useLoadedSettings: () => ({ s: { tmdb_region: "" } }),
  useSetting: () => ({ value: "", dirty: false, set: () => {}, commit: async () => true, revert: () => {}, retry: () => {}, status: "idle", error: null, canRetry: false }),
}));
vi.mock("../../lib/useLiveQuery", () => ({ useLiveRefresh: () => () => {} }));

const key = (id: string, over: Partial<APIKeyStatus> = {}): APIKeyStatus => ({
  id, label: id.toUpperCase(), purpose: "", help_url: "", steps: "", secret: true,
  testable: true, tests_candidate: true, configured: true, source: "settings", hint: "…abcd", env_set: false, ...over,
});

const card = (ref: string, over: Partial<ConnectionItem> = {}): ConnectionItem => ({
  kind: "apikey", ref, card: `apikey-${ref}`, name: ref.toUpperCase(), configured: true, enabled: true, state: "unknown", testable: true, ...over,
});

const metadata = (items: ConnectionItem[]) => vi.spyOn(connectionsApi, "list").mockResolvedValue([
  { id: "search", title: "Search", items: [] },
  { id: "downloads", title: "Downloads", items: [] },
  { id: "metadata", title: "Metadata", items },
  { id: "other", title: "Other", items: [] },
]);

beforeEach(() => {
  clearQueryCache();
  Element.prototype.scrollIntoView = vi.fn();
  vi.spyOn(api, "indexers").mockResolvedValue([]);
  vi.spyOn(api, "downloadClientList").mockResolvedValue({ clients: [] });
  vi.spyOn(api, "settings").mockResolvedValue({ tmdb_region: "" } as AppSettings);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const page = () => render(<MemoryRouter initialEntries={["/settings/connections"]}><ConfirmProvider><ConnectionsSettings /></ConfirmProvider></MemoryRouter>);

// open clicks Edit (or Set up) on a key's card and returns the card.
async function open(ref: string): Promise<HTMLElement> {
  const el = (await screen.findByText(ref.toUpperCase(), { selector: "span" })).closest(`#apikey-${ref}`) as HTMLElement;
  const btn = within(el).queryByRole("button", { name: "Edit" }) ?? within(el).getByRole("button", { name: "Set up" });
  await act(async () => { fireEvent.click(btn); });
  return el;
}

describe("API keys on Connections", () => {
  it("tests a typed value without saving it, and says so", async () => {
    metadata([card("tmdb")]);
    vi.spyOn(api, "apiKeys").mockResolvedValue([key("tmdb")]);
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: false, detail: "TMDB rejected the key; use the v3 API key, not the v4 read access token (this looks like a v4 token)" });
    const save = vi.spyOn(api, "setAPIKey");
    page();
    const el = await open("tmdb");
    fireEvent.change(within(el).getByPlaceholderText(/Enter a new value/), { target: { value: "eyJhbGci" } });
    await act(async () => { fireEvent.click(within(el).getByRole("button", { name: "Test typed" })); });
    expect(test).toHaveBeenCalledWith("tmdb", "eyJhbGci");
    expect(save).not.toHaveBeenCalled();
    expect(within(el).getByText(/looks like a v4 token/)).toBeTruthy();
    expect(within(el).getByText(/typed value, not saved/)).toBeTruthy();
  });

  it("offers Set up and a candidate Test before a key is set at all", async () => {
    metadata([card("omdb", { configured: false, enabled: false, testable: false })]);
    vi.spyOn(api, "apiKeys").mockResolvedValue([key("omdb", { configured: false, source: "", hint: undefined })]);
    page();
    const el = await open("omdb");
    expect(within(el).queryByRole("button", { name: /Test/ })).toBeNull();
    fireEvent.change(within(el).getByPlaceholderText(/Paste your key/), { target: { value: "abc" } });
    expect(within(el).getByRole("button", { name: "Test typed" })).toBeTruthy();
  });

  it("never sends a blank Save", async () => {
    metadata([card("tmdb")]);
    vi.spyOn(api, "apiKeys").mockResolvedValue([key("tmdb")]);
    const save = vi.spyOn(api, "setAPIKey");
    page();
    const el = await open("tmdb");
    fireEvent.change(within(el).getByPlaceholderText(/Enter a new value/), { target: { value: "   " } });
    expect((within(el).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    expect(save).not.toHaveBeenCalled();
  });

  it("tests the saved key straight after Save; the OpenSubtitles password tests through the API key", async () => {
    metadata([card("opensubtitles_api", { name: "OpenSubtitles" })]);
    const keys = [key("opensubtitles_api"), key("opensubtitles_username", { secret: false, testable: false, tests_candidate: false }), key("opensubtitles_password", { testable: false, tests_candidate: false })];
    vi.spyOn(api, "apiKeys").mockResolvedValue(keys);
    vi.spyOn(api, "setAPIKey").mockResolvedValue(keys);
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: true, detail: "OK: signed in as me, 17 downloads left today." });
    page();
    const el = (await screen.findByText("OpenSubtitles", { selector: "span" })).closest("#apikey-opensubtitles_api") as HTMLElement;
    await act(async () => { fireEvent.click(within(el).getByRole("button", { name: "Edit" })); });
    await within(el).findByText("OPENSUBTITLES_PASSWORD");
    fireEvent.change(within(el).getByLabelText("OPENSUBTITLES_PASSWORD"), { target: { value: "hunter2" } });
    await act(async () => { fireEvent.click(within(el).getAllByRole("button", { name: "Save" })[2]); });
    expect(test).toHaveBeenCalledWith("opensubtitles_api", undefined);
    expect(await within(el).findByText(/17 downloads left today/)).toBeTruthy();
  });

  it("asks before clearing a key, and does nothing on Cancel", async () => {
    metadata([card("tmdb")]);
    vi.spyOn(api, "apiKeys").mockResolvedValue([key("tmdb")]);
    const clear = vi.spyOn(api, "clearAPIKey").mockResolvedValue([key("tmdb", { configured: false, source: "" })]);
    page();
    const el = await open("tmdb");
    fireEvent.click(within(el).getByRole("button", { name: "Clear" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Discover, Movies and TV stop finding anything/)).toBeTruthy();
    await act(async () => { fireEvent.click(within(dialog).getByText("Cancel")); });
    expect(clear).not.toHaveBeenCalled();
  });

  it("shows OMDb's last complaint under its key", async () => {
    metadata([card("omdb")]);
    vi.spyOn(api, "apiKeys").mockResolvedValue([
      key("omdb", { last_error: "OMDb: Request limit reached!", last_error_at: new Date(Date.now() - 5 * 60_000).toISOString() }),
    ]);
    page();
    const el = await open("omdb");
    expect(await within(el).findByText(/Last failed 5 min ago: OMDb: Request limit reached!/)).toBeTruthy();
  });
});
