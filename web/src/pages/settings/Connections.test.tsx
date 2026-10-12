// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

import { downloadClientApi } from "../../lib/downloadClientApi";
import { indexerApi } from "../../lib/indexerApi";
import { connectionsApi, type ConnectionItem, type ConnectionSectionData } from "../../lib/connectionsApi";
import { clearQueryCache } from "../../lib/query";
import { ConfirmProvider } from "../../ui";
import { ConnectionsSettings } from "./Connections";

// INT-16: one page of cards, each with a dot and its last-OK or last-error line; Test
// updates its card in place; a failing card offers Fix; a health link's card is outlined.

vi.mock("../../lib/useLiveQuery", () => ({ useLiveRefresh: () => () => {} }));

const item = (over: Partial<ConnectionItem>): ConnectionItem => ({
  kind: "indexer", ref: "1", card: "indexer-1", name: "TorrentLeech", configured: true, enabled: true,
  state: "unknown", testable: true, ...over,
});

const sections = (items: Partial<Record<ConnectionSectionData["id"], ConnectionItem[]>>): ConnectionSectionData[] => [
  { id: "search", title: "Search", items: items.search ?? [] },
  { id: "downloads", title: "Downloads", items: items.downloads ?? [] },
  { id: "metadata", title: "Metadata", items: items.metadata ?? [] },
  { id: "other", title: "Plex, alerts and apps", items: items.other ?? [] },
];

const page = (at = "/settings/connections") =>
  render(<MemoryRouter initialEntries={[at]}><ConfirmProvider><ConnectionsSettings /></ConfirmProvider></MemoryRouter>);

beforeEach(() => {
  clearQueryCache();
  Element.prototype.scrollIntoView = vi.fn();
  vi.spyOn(indexerApi, "indexers").mockResolvedValue([]);
  vi.spyOn(downloadClientApi, "downloadClientList").mockResolvedValue({ clients: [] });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Connections", () => {
  it("shows a card per connection in its section, with its status line", async () => {
    vi.spyOn(connectionsApi, "list").mockResolvedValue(sections({
      search: [item({ kind: "flaresolverr", ref: "default", card: "flaresolverr", name: "FlareSolverr", state: "ok", last_ok_at: new Date().toISOString() }), item({})],
      downloads: [item({ kind: "download_client", card: "download_client-1", name: "qBittorrent", state: "failing", last_error: "connection refused", failing_since: new Date().toISOString() })],
      metadata: [item({ kind: "apikey", ref: "tvdb", card: "apikey-tvdb", name: "TheTVDB", configured: false, testable: false })],
    }));
    page();
    const fs = (await screen.findByText("FlareSolverr")).closest("#flaresolverr") as HTMLElement;
    expect(within(fs).getByText(/Last OK/)).toBeTruthy();
    const qb = document.getElementById("download_client-1") as HTMLElement;
    expect(within(qb).getByText(/Failing since .*: connection refused/)).toBeTruthy();
    expect(within(qb).getByRole("img", { name: "Failing" })).toBeTruthy();
    const tvdb = document.getElementById("apikey-tvdb") as HTMLElement;
    expect(within(tvdb).getByText("Not set up")).toBeTruthy();
    expect(within(tvdb).queryByRole("button", { name: "Test" })).toBeNull();
    expect(document.getElementById("search")).toBeTruthy();
    expect(document.getElementById("download-clients")).toBeTruthy();
  });

  it("tests a connection and updates its card in place", async () => {
    vi.spyOn(connectionsApi, "list").mockResolvedValue(sections({ downloads: [item({ kind: "download_client", card: "download_client-1", name: "qBittorrent" })] }));
    const test = vi.spyOn(connectionsApi, "test").mockResolvedValue({
      ok: false, detail: "connection refused",
      item: item({ kind: "download_client", card: "download_client-1", name: "qBittorrent", state: "failing", last_error: "connection refused", last_error_at: new Date().toISOString() }),
    });
    page();
    const card = (await screen.findByText("qBittorrent")).closest("#download_client-1") as HTMLElement;
    await act(async () => { fireEvent.click(within(card).getByRole("button", { name: "Test" })); });
    expect(test).toHaveBeenCalledWith("download_client", "1");
    expect(within(card).getByRole("status").textContent).toContain("connection refused");
    expect(within(card).getByRole("img", { name: "Failing" })).toBeTruthy();
  });

  it("outlines the card a link points at", async () => {
    vi.spyOn(connectionsApi, "list").mockResolvedValue(sections({ search: [item({ card: "indexer-1" }), item({ ref: "2", card: "indexer-2", name: "1337x" })] }));
    page("/settings/connections#indexer-2");
    await screen.findByText("1337x");
    expect((document.getElementById("indexer-2") as HTMLElement).style.border).toContain("var(--accent)");
    expect((document.getElementById("indexer-1") as HTMLElement).style.border).not.toContain("var(--accent)");
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
  });

  it("says what went wrong when the list can't load", async () => {
    vi.spyOn(connectionsApi, "list").mockRejectedValue(new Error("server down"));
    page();
    expect(await screen.findByText(/server down/)).toBeTruthy();
  });
});
