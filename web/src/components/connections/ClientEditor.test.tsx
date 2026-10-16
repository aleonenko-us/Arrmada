// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { type DownloadClient } from "../../lib/api";
import { downloadClientApi, type UsenetFacts } from "../../lib/downloadClientApi";
import { ClientEditor, ClientForm, serversLine } from "./ClientEditor";

// Usenet U4: SABnzbd is added beside qBittorrent with its URL and API key (sent once, never
// shown back), and its card says what Arrmada needs from it: categories (added only on a
// click), the completed folder Arrmada sees, the news servers and anything to fix.

const KEY = "0f1e2d3c4b5a69788796a5b4c3d2e1f0";
const sab = (over: Partial<DownloadClient> = {}): DownloadClient => ({ id: 4, name: "SABnzbd", kind: "sabnzbd", url: "http://sabnzbd:8080", enabled: true, priority: 25, ...over });
const facts = (over: Partial<UsenetFacts> = {}): UsenetFacts => ({
  version: "4.5.5",
  categories: [
    { name: "arrmada", for: "movies", present: true },
    { name: "arrmada-tv", for: "tv", present: false },
  ],
  missing_categories: ["arrmada-tv"],
  complete_folder: { what: "complete_folder", remote: "/downloads/complete", local: "/media/downloads/complete", visible: true },
  servers: [{ name: "news.example", optional: false, connections: 20, ok: true }],
  servers_configured: 1,
  servers_working: 1,
  language: "en",
  not_english: false,
  warnings: ['SABnzbd is missing Arrmada\'s arrmada-tv category. Use "Add missing categories"'],
  ...over,
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("adding SABnzbd", () => {
  it("picks the kind, asks only for URL and API key, and sends the key as the password", async () => {
    const create = vi.spyOn(downloadClientApi, "createDownloadClient").mockResolvedValue(sab());
    const saved = vi.fn();
    render(<ClientForm onSaved={saved} />);
    const form = screen.getByRole("form", { name: "Add a download client" });
    fireEvent.click(within(form).getByRole("radio", { name: /SABnzbd/ }));
    expect((within(form).getByLabelText("Name") as HTMLInputElement).value).toBe("SABnzbd");
    expect(within(form).queryByLabelText("Username")).toBeNull();
    expect(within(form).getByText("SABnzbd → Config → General → API Key")).toBeTruthy();
    fireEvent.change(within(form).getByLabelText("URL"), { target: { value: "http://sabnzbd:8080" } });
    fireEvent.change(within(form).getByLabelText("API key"), { target: { value: KEY } });
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Save client" })); });
    expect(create).toHaveBeenCalledWith({ name: "SABnzbd", kind: "sabnzbd", url: "http://sabnzbd:8080", username: "", password: KEY, priority: 25 });
    expect(saved).toHaveBeenCalled();
    // The key isn't left on screen once it's saved.
    expect((within(form).getByLabelText("API key") as HTMLInputElement).value).toBe("");
  });

  it("keeps the stored key when an edit leaves it blank", async () => {
    vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 0, usenet: facts() });
    const upd = vi.spyOn(downloadClientApi, "updateDownloadClient").mockResolvedValue(sab());
    render(<ClientEditor client={sab()} onSaved={() => {}} />);
    const form = screen.getByRole("form", { name: "Edit SABnzbd" });
    expect(within(form).queryByRole("radio")).toBeNull();
    expect((within(form).getByLabelText("API key") as HTMLInputElement).value).toBe("");
    await act(async () => { fireEvent.click(within(form).getByRole("button", { name: "Save changes" })); });
    expect(upd).toHaveBeenCalledWith(4, expect.objectContaining({ kind: "sabnzbd", password: "" }));
  });
});

describe("the SABnzbd card", () => {
  it("reads the facts once and adds the missing categories only on the click", async () => {
    const status = vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 0, usenet: facts() });
    const add = vi.spyOn(downloadClientApi, "addMissingCategories").mockResolvedValue({
      created: ["arrmada-tv"],
      usenet: facts({ categories: [{ name: "arrmada", for: "movies", present: true }, { name: "arrmada-tv", for: "tv", present: true }], missing_categories: [], warnings: [] }),
    });
    const changed = vi.fn();
    render(<ClientEditor client={sab()} onSaved={() => {}} onChanged={changed} />);
    const card = await screen.findByRole("region", { name: "SABnzbd" });
    expect(status).toHaveBeenCalledTimes(1);
    const cats = within(card).getByRole("list", { name: "Categories" });
    expect(cats.textContent).toMatch(/✓ arrmada \(movies\)/);
    expect(cats.textContent).toMatch(/✕ arrmada-tv \(TV\)/);
    expect(within(card).getByText(/Arrmada sees it at/).parentElement?.textContent).toMatch(/✓ Completed folder \/downloads\/complete · Arrmada sees it at \/media\/downloads\/complete/);
    expect(within(card).getByText(/1 server, working/)).toBeTruthy();
    expect(within(card).getByRole("list", { name: "Needs a look" }).textContent).toMatch(/missing Arrmada's arrmada-tv/);
    expect(add).not.toHaveBeenCalled();

    await act(async () => { fireEvent.click(within(card).getByRole("button", { name: "Add missing categories" })); });
    expect(add).toHaveBeenCalledWith(4);
    expect(within(card).getByRole("status").textContent).toBe("Added arrmada-tv.");
    expect(within(card).queryByRole("button", { name: "Add missing categories" })).toBeNull();
    expect(within(card).queryByRole("list", { name: "Needs a look" })).toBeNull();
    expect(changed).toHaveBeenCalled();
    expect(status).toHaveBeenCalledTimes(1);
  });

  it("says plainly when SABnzbd couldn't be read", async () => {
    vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 0, usenet_error: "API Key Incorrect" });
    render(<ClientEditor client={sab()} onSaved={() => {}} />);
    expect((await screen.findByRole("alert")).textContent).toBe("Couldn't read SABnzbd: API Key Incorrect");
  });

  // U5: the bundled SABnzbd arrives without a news server; its card links to SABnzbd's own
  // page on this host, where the provider is added. Someone's own SABnzbd gets no link.
  it("links the bundled SABnzbd's own page, saying to add the provider while it has none", async () => {
    vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 0, usenet: facts({ servers: [], servers_configured: 0, servers_working: 0 }) });
    render(<ClientEditor client={sab({ bundled: true, ui_port: "8086" })} onSaved={() => {}} />);
    const link = await screen.findByRole("link", { name: /:8086/ });
    expect(link.getAttribute("href")).toBe(`${window.location.protocol}//${window.location.hostname}:8086`);
    expect(screen.getByText(/Add your Usenet provider in SABnzbd/)).toBeTruthy();
    cleanup();
    render(<ClientEditor client={sab()} onSaved={() => {}} />);
    await screen.findByText(/Usenet servers:/);
    expect(screen.queryByRole("link", { name: /↗/ })).toBeNull();
  });
});

describe("serversLine", () => {
  it("sums up the news servers", () => {
    expect(serversLine({ servers_configured: 1, servers_working: 1 })).toEqual({ text: "1 server, working", ok: true });
    expect(serversLine({ servers_configured: 3, servers_working: 3 })).toEqual({ text: "3 servers, all working", ok: true });
    expect(serversLine({ servers_configured: 2, servers_working: 1 })).toEqual({ text: "2 servers, 1 working", ok: false });
    expect(serversLine({ servers_configured: 0, servers_working: 0 })).toEqual({ text: "No news server set up", ok: false });
  });
});

// The Unraid all-in-one image's qBittorrent: its card says it's built in, links its own
// page on the published port, and an admin (only) can show the login Arrmada made.
describe("built-in qBittorrent", () => {
  const qb = (over: Partial<DownloadClient> = {}): DownloadClient => ({ id: 1, name: "qBittorrent (bundled)", kind: "qbittorrent", url: "http://127.0.0.1:8080", enabled: true, priority: 25, bundled: true, ui_port: "8091", has_login: true, ...over });

  it("links its page and shows the login to an admin on request", async () => {
    vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 56881 });
    const login = vi.spyOn(downloadClientApi, "bundledLogin").mockResolvedValue({ username: "admin", password: "s3cret-Pass_word" });
    render(<ClientEditor client={qb()} onSaved={() => {}} admin />);
    expect(screen.getByText(/Built into this container/)).toBeTruthy();
    const link = screen.getByRole("link", { name: /:8091/ }) as HTMLAnchorElement;
    expect(link.href).toBe(`${window.location.protocol}//${window.location.hostname}:8091/`);
    expect(screen.queryByText("s3cret-Pass_word")).toBeNull();
    expect(login).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Show its login" })); });
    expect(login).toHaveBeenCalledOnce();
    expect(screen.getByText("s3cret-Pass_word")).toBeTruthy();
  });

  it("doesn't offer the login to a manager, or appear on the Compose bundle's card", () => {
    vi.spyOn(downloadClientApi, "downloadClientStatus").mockResolvedValue({ listen_port: 6881 });
    render(<ClientEditor client={qb()} onSaved={() => {}} />);
    expect(screen.queryByRole("button", { name: "Show its login" })).toBeNull();
    cleanup();
    render(<ClientEditor client={qb({ url: "http://arrmada-qbittorrent:8080", ui_port: undefined, has_login: undefined })} onSaved={() => {}} admin />);
    expect(screen.queryByText(/Built into this container/)).toBeNull();
  });
});
