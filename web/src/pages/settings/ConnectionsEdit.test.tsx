// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api, type DownloadClient, type Indexer } from "../../lib/api";
import { connectionsApi, type ConnectionItem } from "../../lib/connectionsApi";
import { clearQueryCache } from "../../lib/query";
import { ConfirmProvider } from "../../ui";
import { ConnectionsSettings } from "./Connections";

// INT-17 (with INT-04/05/10/11's fixes carried over): indexers and download clients are
// added, edited, switched off and deleted from their cards on Settings → Connections.

vi.mock("../../lib/useLive", () => ({ useLive: () => ({ connected: false, last: null }) }));

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
  vi.spyOn(api, "indexers").mockResolvedValue(indexers);
  vi.spyOn(api, "downloadClientList").mockResolvedValue({ clients, categories: CATS, can_restore_bundled: canRestore });
}

beforeEach(() => {
  clearQueryCache();
  Element.prototype.scrollIntoView = vi.fn();
  vi.spyOn(api, "prowlarrInfo").mockResolvedValue({ url: "", has_key: false });
  vi.spyOn(api, "downloadClientStatus").mockResolvedValue({ listen_port: 0 });
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
  it("names a new indexer after its kind until the user types a name, in one column on a phone", async () => {
    serve({});
    page();
    fireEvent.click(await screen.findByRole("button", { name: "+ Add indexer" }));
    const form = screen.getByRole("form", { name: "Add an indexer" });
    const name = within(form).getByDisplayValue("1337x") as HTMLInputElement;
    const kind = within(form).getByRole("combobox") as HTMLSelectElement;
    fireEvent.change(kind, { target: { value: "torrentleech" } });
    expect(name.value).toBe("TorrentLeech");
    fireEvent.change(name, { target: { value: "My tracker" } });
    fireEvent.change(kind, { target: { value: "torznab" } });
    expect(name.value).toBe("My tracker");
    const grid = kind.closest(".grid") as HTMLElement;
    expect(grid.className).toContain("grid-cols-1");
    expect(grid.className).toContain("sm:grid-cols-2");
    expect(within(form).getByText(/Only breaks ties/)).toBeTruthy();
  });
});

describe("an indexer's card", () => {
  it("asks before deleting and does nothing on Cancel", async () => {
    serve({ indexers: [idx()] });
    const del = vi.spyOn(api, "deleteIndexer").mockResolvedValue(undefined);
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
    vi.spyOn(api, "deleteIndexer").mockRejectedValue(new Error("database is locked"));
    page();
    const card = await cardOf("indexer-1");
    await menuPick(card, "TorrentLeech", "Delete");
    await act(async () => { fireEvent.click(within(screen.getByRole("dialog")).getByText("Delete")); });
    expect(within(card).getByRole("alert").textContent).toMatch(/Couldn't delete: database is locked/);
  });

  it("Disable saves the indexer switched off, keeping its saved key", async () => {
    serve({ indexers: [idx({ prowlarr_id: 4, kind: "torznab", url: "http://p/4/api" })] });
    const upd = vi.spyOn(api, "updateIndexer").mockResolvedValue(undefined);
    page();
    const card = await cardOf("indexer-1");
    expect(within(card).getByText("Managed by Prowlarr")).toBeTruthy();
    await menuPick(card, "TorrentLeech", "Disable");
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ enabled: false, name: "TorrentLeech", url: "http://p/4/api" }));
    expect(upd.mock.calls[0][1].api_key).toBeUndefined();
  });

  it("opens the editor: tie-break explained, and the 'Used for' pills read 'searched for this area'", async () => {
    serve({ indexers: [idx()] });
    const upd = vi.spyOn(api, "updateIndexer").mockResolvedValue(undefined);
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
    const upd = vi.spyOn(api, "updateIndexer");
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
    const del = vi.spyOn(api, "deleteDownloadClient").mockResolvedValue(undefined);
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
    const upd = vi.spyOn(api, "updateDownloadClient").mockRejectedValue(new Error("download client not found"));
    page();
    const card = await cardOf("download_client-1");
    await menuPick(card, "qBittorrent", "Disable");
    expect(upd).toHaveBeenCalledWith(1, expect.objectContaining({ enabled: false, password: "" }));
    expect(within(card).getByRole("alert").textContent).toMatch(/Couldn't disable it: download client not found/);
  });

  it("edits in place — blank password kept, order checked — then tests it", async () => {
    serve({ clients: [client({ username: "admin" })] });
    const upd = vi.spyOn(api, "updateDownloadClient").mockResolvedValue(client({ name: "Home qB" }));
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
    const restore = vi.spyOn(api, "restoreBundledClient").mockResolvedValue({ restored: true });
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
