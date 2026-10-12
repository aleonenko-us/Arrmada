// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { type DownloadClient, type Indexer } from "../../lib/api";
import { downloadClientApi } from "../../lib/downloadClientApi";
import { indexerApi } from "../../lib/indexerApi";
import { connectionsApi, type ConnectionItem } from "../../lib/connectionsApi";
import { clearQueryCache } from "../../lib/query";
import { ConfirmProvider } from "../../ui";
import { ConnectionsSettings } from "./Connections";

// INT-17 (with INT-04/05/10/11's fixes carried over): indexers and download clients are
// added, edited, switched off and deleted from their cards on Settings → Connections.

vi.mock("../../lib/useLiveQuery", () => ({ useLiveRefresh: () => () => {} }));

const idx = (over: Partial<Indexer> = {}): Indexer => ({ id: 1, name: "TorrentLeech", kind: "torrentleech", priority: 25, enabled: true, ...over });
const client = (over: Partial<DownloadClient> = {}): DownloadClient => ({ id: 1, name: "qBittorrent", kind: "qbittorrent", url: "http://qb:8080", enabled: true, priority: 25, ...over });
const CATS = { movies: "arrmada", tv: "arrmada-tv", books: "arrmada-books", music: "arrmada-music" };

const idxCard = (i: Indexer): ConnectionItem => ({
  kind: "indexer", ref: String(i.id), card: `indexer-${i.id}`, name: i.name, type: i.kind, configured: true, enabled: i.enabled,
  state: i.enabled ? "unknown" : "disabled", testable: true, managed_by: i.prowlarr_id ? "prowlarr" : undefined,
});
const clientCard = (c: DownloadClient): ConnectionItem => ({
  kind: "download_client", ref: String(c.id), card: `download_client-${c.id}`, name: c.name, type: c.kind, configured: true, enabled: c.enabled,
  state: c.enabled ? "unknown" : "disabled", testable: true,
});

function serve({ indexers = [], clients = [], canRestore = false }: { indexers?: Indexer[]; clients?: DownloadClient[]; canRestore?: boolean }) {
  vi.spyOn(connectionsApi, "list").mockResolvedValue([
    { id: "search", title: "Search", items: [
      { kind: "prowlarr", ref: "default", card: "prowlarr", name: "Prowlarr", configured: false, enabled: false, state: "unknown", testable: false },
      ...indexers.map(idxCard),
    ] },
    { id: "downloads", title: "Downloads", items: clients.map(clientCard) },
    { id: "metadata", title: "Metadata", items: [] },
    { id: "other", title: "Other", items: [] },
  ]);
  vi.spyOn(indexerApi, "indexers").mockResolvedValue(indexers);
  vi.spyOn(downloadClientApi, "downloadClientList").mockResolvedValue({ clients, categories: CATS, can_restore_bundled: canRestore });
}

beforeEach(() => {
  clearQueryCache();
  Element.prototype.scrollIntoView = vi.fn();
  vi.spyOn(indexerApi, "prowlarrInfo").mockResolvedValue({ url: "", has_key: false });
  vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 0 });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const page = () => render(<MemoryRouter initialEntries={["/settings/connections"]}><ConfirmProvider><ConnectionsSettings /></ConfirmProvider></MemoryRouter>);
const cardOf = async (id: string) => {
  await screen.findAllByText(/./, { selector: `#${id} span` });
  return document.getElementById(id) as HTMLElement;
};
const menuPick = async (card: HTMLElement, name: string, item: string) => {
  fireEvent.click(within(card).getByRole("button", { name: `More for ${name}` }));
  await act(async () => { fireEvent.click(within(card).getByRole("menuitem", { name: item })); });
};

describe("adding an indexer", () => {
  // INT-18: a catalogue of presets, each showing only what its tracker needs, with a Test
  // before any row exists.
  const openCatalogue = async () => {
    fireEvent.click(await screen.findByRole("button", { name: "+ Add indexer" }));
    return screen.findByRole("dialog", { name: "Add an indexer" });
  };
  const pick = async (label: RegExp) => {
    const dialog = await openCatalogue();
    fireEvent.click(within(dialog).getByRole("button", { name: label }));
    return screen.getByRole("form", { name: "Add an indexer" });
  };

  it("offers the presets as monogram tiles, with no Newznab", async () => {
    serve({});
    page();
    const dialog = await openCatalogue();
    const tiles = within(dialog).getAllByRole("button").map((b) => b.textContent ?? "");
    for (const t of ["TorrentLeech", "MyAnonaMouse", "1337x", "Prowlarr", "Jackett / other Torznab"]) {
      expect(tiles.some((x) => x.includes(t))).toBe(true);
    }
    expect(tiles.some((x) => /newznab|usenet/i.test(x))).toBe(false);
    expect(dialog.querySelector("img")).toBeNull();
    // Two tiles a row from 400px, one on a 375px phone.
    expect((dialog.querySelector("ul") as HTMLElement).className).toMatch(/grid-cols-1 .*min-\[400px\]:grid-cols-2/);
  });

  it("TorrentLeech asks only for its login and seed rules, tests it, then saves it named TorrentLeech", async () => {
    serve({});
    const test = vi.spyOn(indexerApi, "testIndexerSettings").mockResolvedValue({ ok: true });
    const create = vi.spyOn(indexerApi, "createIndexer").mockResolvedValue(idx({ id: 9 }));
    page();
    const form = await pick(/TorrentLeech/);
    expect(within(form).queryByLabelText("Feed URL")).toBeNull();
    expect(within(form).queryByLabelText("API key")).toBeNull();
    expect(within(form).queryByLabelText(/Site URL/)).toBeNull();
    expect(within(form).getByText("Seed rules")).toBeTruthy();
    expect(within(form).getByText(/private tracker counts how long you seed/)).toBeTruthy();
    // Nothing to test until the login is filled in.
    expect((within(form).getByRole("button", { name: "Test" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(within(form).getByLabelText("Username"), { target: { value: "me" } });
    fireEvent.change(within(form).getByLabelText("Password"), { target: { value: "pw" } });
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Test" })); });
    expect(test).toHaveBeenCalledWith(expect.objectContaining({ kind: "torrentleech", name: "TorrentLeech", username: "me", password: "pw", seed_enabled: true, seed_hours: 14 * 24, media_types: [] }));
    expect(test.mock.calls[0][0].url).toBeUndefined();
    expect(within(form).getByText(/Connected/)).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Save indexer" })); });
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ kind: "torrentleech", name: "TorrentLeech", username: "me" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("a Torznab feed with a bad URL fails the test with the reason, and nothing is saved", async () => {
    serve({});
    vi.spyOn(indexerApi, "testIndexerSettings").mockResolvedValue({ ok: false, error: "That's a web page, not a Torznab feed" });
    const create = vi.spyOn(indexerApi, "createIndexer");
    page();
    const form = await pick(/Jackett/);
    fireEvent.change(within(form).getByLabelText("Feed URL"), { target: { value: "http://jackett:9117/" } });
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Test" })); });
    expect(within(form).getByText(/web page, not a Torznab feed/)).toBeTruthy();
    expect(within(form).queryByRole("button", { name: "Save indexer" })).toBeNull();
    expect(within(form).getByRole("button", { name: "Save without testing" })).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
  });

  it("a passing test goes stale when the form changes", async () => {
    serve({});
    vi.spyOn(indexerApi, "testIndexerSettings").mockResolvedValue({ ok: true, caps_summary: "Movies (imdbid)" });
    page();
    const form = await pick(/Jackett/);
    const url = within(form).getByLabelText("Feed URL");
    fireEvent.change(url, { target: { value: "http://jackett/api" } });
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Test" })); });
    expect(within(form).getByText(/Movies \(imdbid\)/)).toBeTruthy();
    fireEvent.change(url, { target: { value: "http://jackett/other" } });
    expect(within(form).queryByText(/Connected/)).toBeNull();
    expect(within(form).queryByRole("button", { name: "Save indexer" })).toBeNull();
  });

  it("can still save without testing", async () => {
    serve({});
    const test = vi.spyOn(indexerApi, "testIndexerSettings");
    const create = vi.spyOn(indexerApi, "createIndexer").mockResolvedValue(idx({ id: 9, kind: "1337x", name: "1337x" }));
    page();
    const form = await pick(/1337x/);
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Save without testing" })); });
    expect(test).not.toHaveBeenCalled();
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ kind: "1337x", name: "1337x", url: "", media_types: ["movie", "series"] }));
  });

  it("MyAnonaMouse defaults to Books only", async () => {
    serve({});
    page();
    const form = await pick(/MyAnonaMouse/);
    const scope = within(form).getByRole("group", { name: "Used for" });
    const pressed = within(scope).getAllByRole("button").filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.textContent);
    expect(pressed).toEqual(["Books"]);
    expect(within(form).getByLabelText("mam_id session")).toBeTruthy();
  });

  it("the Prowlarr tile opens the Prowlarr card instead of a form", async () => {
    serve({});
    page();
    const dialog = await openCatalogue();
    fireEvent.click(within(dialog).getByRole("button", { name: /Prowlarr/ }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const card = document.getElementById("prowlarr") as HTMLElement;
    expect(await within(card).findByText(/Pull your Prowlarr indexers in/)).toBeTruthy();
  });
});

describe("an indexer's card", () => {
  it("asks before deleting and does nothing on Cancel", async () => {
    serve({ indexers: [idx()] });
    const del = vi.spyOn(indexerApi, "deleteIndexer").mockResolvedValue(undefined);
    page();
    const card = await cardOf("indexer-1");
    await menuPick(card, "TorrentLeech", "Delete");
    const dialog = screen.getByRole("dialog", { name: "Delete TorrentLeech?" });
    expect(within(dialog).getByText(/14-day default/)).toBeTruthy();
    await act(async () => { fireEvent.click(within(dialog).getByText("Cancel")); });
    expect(del).not.toHaveBeenCalled();
  });

  it("shows the server's error when the delete fails", async () => {
    serve({ indexers: [idx()] });
    vi.spyOn(indexerApi, "deleteIndexer").mockRejectedValue(new Error("database is locked"));
    page();
    const card = await cardOf("indexer-1");
    await menuPick(card, "TorrentLeech", "Delete");
    await act(async () => { fireEvent.click(within(screen.getByRole("dialog")).getByText("Delete")); });
    expect(within(card).getByRole("alert").textContent).toMatch(/Couldn't delete: database is locked/);
  });

  it("Disable saves the indexer switched off, keeping its saved key", async () => {
    serve({ indexers: [idx({ prowlarr_id: 4, kind: "torznab", url: "http://p/4/api" })] });
    const upd = vi.spyOn(indexerApi, "updateIndexer").mockResolvedValue(undefined);
    page();
    const card = await cardOf("indexer-1");
    expect(within(card).getByText("Managed by Prowlarr")).toBeTruthy();
    await menuPick(card, "TorrentLeech", "Disable");
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ enabled: false, name: "TorrentLeech", url: "http://p/4/api" }));
    expect(upd.mock.calls[0][1].api_key).toBeUndefined();
  });

  it("opens the editor: tie-break explained, and the 'Used for' pills read 'searched for this area'", async () => {
    serve({ indexers: [idx()] });
    const upd = vi.spyOn(indexerApi, "updateIndexer").mockResolvedValue(undefined);
    page();
    const card = await cardOf("indexer-1");
    fireEvent.click(within(card).getByRole("button", { name: "Edit" }));
    expect(within(card).getByText("tie-break 25").getAttribute("title")).toMatch(/Only breaks ties/);
    const pill = (l: string) => within(card).getByRole("button", { name: l });
    expect(["Movies", "TV", "Books", "Music"].every((l) => pill(l).getAttribute("aria-pressed") === "true")).toBe(true);
    await act(async () => { fireEvent.click(pill("Music")); });
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ media_types: ["movie", "series", "book"] }));
  });

  it("won't turn off the last lit pill", async () => {
    serve({ indexers: [idx({ media_types: ["book"] })] });
    const upd = vi.spyOn(indexerApi, "updateIndexer");
    page();
    const card = await cardOf("indexer-1");
    fireEvent.click(within(card).getByRole("button", { name: "Edit" }));
    await act(async () => { fireEvent.click(within(card).getByRole("button", { name: "Books" })); });
    expect(upd).not.toHaveBeenCalled();
    expect(within(card).getByRole("alert").textContent).toMatch(/Disable the indexer instead/);
  });
});

describe("a download client's card", () => {
  it("asks before removing, naming what it leaves alone; a bundled one says it stays removed", async () => {
    serve({ clients: [client({ bundled: true })] });
    const del = vi.spyOn(downloadClientApi, "deleteDownloadClient").mockResolvedValue(undefined);
    page();
    const card = await cardOf("download_client-1");
    await menuPick(card, "qBittorrent", "Delete");
    const dialog = screen.getByRole("dialog", { name: "Remove qBittorrent?" });
    expect(within(dialog).getByText(/Torrents already there are untouched/)).toBeTruthy();
    expect(within(dialog).getByText(/stays removed after a restart; you can restore it/)).toBeTruthy();
    await act(async () => { fireEvent.click(within(dialog).getByText("Cancel")); });
    expect(del).not.toHaveBeenCalled();
  });

  it("Disable saves via PUT with a blank password (kept), and a failure shows on the card", async () => {
    serve({ clients: [client()] });
    const upd = vi.spyOn(downloadClientApi, "updateDownloadClient").mockRejectedValue(new Error("download client not found"));
    page();
    const card = await cardOf("download_client-1");
    await menuPick(card, "qBittorrent", "Disable");
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ enabled: false, password: "" }));
    expect(within(card).getByRole("alert").textContent).toMatch(/Couldn't disable it: download client not found/);
  });

  it("edits in place — blank password kept, order checked — then tests it", async () => {
    serve({ clients: [client({ username: "admin" })] });
    const upd = vi.spyOn(downloadClientApi, "updateDownloadClient").mockResolvedValue(client({ name: "Home qB" }));
    const test = vi.spyOn(connectionsApi, "test").mockResolvedValue({ ok: true, detail: "Connected", item: clientCard(client({ name: "Home qB" })) });
    page();
    const card = await cardOf("download_client-1");
    fireEvent.click(within(card).getByRole("button", { name: "Edit" }));
    expect((within(card).getByPlaceholderText(/leave blank to keep/) as HTMLInputElement).value).toBe("");
    const order = within(card).getByLabelText(/Order \(1 = first\)/) as HTMLInputElement;
    fireEvent.change(order, { target: { value: "0" } });
    await act(async () => { fireEvent.submit(order.closest("form")!); });
    expect(upd).not.toHaveBeenCalled();
    expect(within(card).getByText(/Order must be a whole number from 1 to 99/)).toBeTruthy();
    fireEvent.change(order, { target: { value: "5" } });
    fireEvent.change(within(card).getByDisplayValue("qBittorrent"), { target: { value: "Home qB" } });
    await act(async () => { fireEvent.click(within(card).getByText("Save changes")); });
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ name: "Home qB", username: "admin", password: "", enabled: true, priority: 5 }));
    await waitFor(() => expect(test).toHaveBeenCalledWith("download_client", "1"));
  });

  it("adds a client with no default URL, lists the fixed categories, and offers the bundled one back", async () => {
    serve({ canRestore: true });
    const restore = vi.spyOn(downloadClientApi, "restoreBundledClient").mockResolvedValue({ restored: true });
    page();
    await screen.findByText(/No download clients yet/);
    expect(screen.getByText("arrmada-tv")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "+ Add client" }));
    const form = screen.getByRole("form", { name: "Add a download client" });
    expect((within(form).getByPlaceholderText(/as reached from the Arrmada container/) as HTMLInputElement).value).toBe("");
    expect(within(form).queryByText("Category")).toBeNull();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Restore bundled qBittorrent" })); });
    expect(restore).toHaveBeenCalled();
  });
});

describe("the Prowlarr card", () => {
  it("opens the sync form from Set up", async () => {
    serve({});
    page();
    const card = await cardOf("prowlarr");
    fireEvent.click(within(card).getByRole("button", { name: "Set up" }));
    expect(within(card).getByRole("button", { name: "Sync now" })).toBeTruthy();
    expect(within(card).getByText(/Prowlarr URL/)).toBeTruthy();
  });
});
