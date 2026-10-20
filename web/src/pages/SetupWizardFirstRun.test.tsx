// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api, ApiError, type APIKeyStatus, type SetupState, type Status } from "../lib/api";
import { setupApi } from "../lib/setupApi";
import { clearQueryCache } from "../lib/query";
import { ConfirmProvider } from "../ui";
import { passwordHint } from "../components/setup/AccountStep";
import { SetupWizard } from "./SetupWizard";

// First run: no account exists, so the wizard opens a setup session, goes media server, keys,
// folders, indexers, and makes the account LAST.

const paths = { movies: "", tv: "", ebooks: "", audiobooks: "", music: "", downloads: "" };
const tmdbKey: APIKeyStatus = { id: "tmdb", label: "TMDB", purpose: "", help_url: "", steps: "", secret: true, testable: true, tests_candidate: true, configured: false, source: "", env_set: false };
const state: SetupState = {
  needed: true, complete: false, tmdb_configured: false, libraries_chosen: false, keys: [tmdbKey],
  library: paths, running: paths, restart_needed: false, can_restart: false, mounts: [], suggestions: { movies: "/media/movies", tv: "/media/tv", downloads: "/media/downloads" },
};
const status = (over: Partial<Status> = {}): Status =>
  ({ needs_setup: true, setup_blocked: false, plex_login: false, jellyfin_login: false, ...over }) as Status;

let startSession: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  startSession = vi.spyOn(setupApi, "startSession").mockResolvedValue({ expires_at: "" });
  vi.spyOn(api, "setupState").mockResolvedValue(state);
  vi.spyOn(api, "status").mockResolvedValue(status());
  vi.spyOn(api, "checkLibraryFolder").mockReturnValue(new Promise(() => {}));
  vi.spyOn(api, "setLibraryPaths").mockResolvedValue(paths);
  vi.spyOn(setupApi, "stateWithProwlarr").mockResolvedValue({ ...state, indexers_enabled: 0, prowlarr_available: false });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  clearQueryCache();
});

const page = (onSignedIn = vi.fn()) => {
  render(<MemoryRouter><ConfirmProvider><SetupWizard firstRun onDone={() => {}} onSignedIn={onSignedIn} /></ConfirmProvider></MemoryRouter>);
  return onSignedIn;
};
async function click(text: string | RegExp) {
  const el = await screen.findByText(text);
  await act(async () => { fireEvent.click(el); });
}
const type = async (label: string | RegExp, value: string) => {
  await act(async () => { fireEvent.change(await screen.findByLabelText(label), { target: { value } }); });
};
// Media (skip) -> Keys -> Folders -> Indexers -> Account.
async function toAccount() {
  await click("Skip this step"); // media
  await click("Continue"); // keys
  await click("Save and continue"); // folders
  await click("Skip this step"); // indexers
  expect(await screen.findByText("Create your account")).toBeTruthy();
}

describe("SetupWizard on a fresh install", () => {
  it("starts a setup session before loading anything, and opens on the media server", async () => {
    page();
    expect(await screen.findByText("Welcome to Arrmada")).toBeTruthy();
    expect(startSession).toHaveBeenCalledOnce();
    expect(screen.getByText("Which media server do you use?")).toBeTruthy();
    expect(screen.getByRole("radio", { name: /^Emby/ })).toBeTruthy();
  });

  it("lists the steps with the account last", async () => {
    page();
    await screen.findByText("Welcome to Arrmada");
    const steps = within(screen.getByRole("list", { name: "Setup steps" })).getAllByRole("listitem").map((li) => li.textContent);
    expect(steps.map((t) => t?.replace("›", "").trim())).toEqual(["1. Media server", "2. Keys", "3. Folders", "4. Indexers", "5. Account"]);
  });

  it("goes media, keys, folders, indexers, then the account, with no account made before", async () => {
    const create = vi.spyOn(setupApi, "createAccount");
    page();
    await toAccount();
    expect(create).not.toHaveBeenCalled();
  });

  it("offers only the password form when no media server sign-in is usable", async () => {
    page();
    await toAccount();
    expect(screen.queryByRole("button", { name: /Sign in with Plex/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Sign in with Jellyfin/ })).toBeNull();
    expect(screen.queryByRole("tab")).toBeNull();
    expect(await screen.findByLabelText("Email")).toBeTruthy();
  });

  it("offers the media server sign-in beside the password form when /status says it is usable", async () => {
    vi.spyOn(api, "status").mockResolvedValue(status({ plex_login: true }));
    page();
    await toAccount();
    expect(await screen.findByRole("button", { name: /Sign in with Plex/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Sign in with Jellyfin/ })).toBeNull();
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["Media server", "Email and password"]);
    await click("Email and password");
    expect(await screen.findByLabelText("Confirm password")).toBeTruthy();
  });

  it("will not create the account until the password is long enough and confirmed", async () => {
    const create = vi.spyOn(setupApi, "createAccount").mockResolvedValue({ user: { id: 1, username: "a@b.co", role: "admin" } as never });
    const signedIn = page();
    await toAccount();
    const submit = (await screen.findByRole("button", { name: "Create account and open Arrmada" })) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await type("Email", "me@home.lan");
    await type("Password", "short");
    await type("Confirm password", "short");
    expect(screen.getByText(/3 more characters to go/)).toBeTruthy();
    expect(submit.disabled).toBe(true);
    await type("Password", "correct horse battery");
    expect(screen.getByText("The two passwords don't match.")).toBeTruthy();
    expect(submit.disabled).toBe(true);
    await type("Confirm password", "correct horse battery");
    expect(screen.getByText("Strong.")).toBeTruthy();
    expect(submit.disabled).toBe(false);
    await act(async () => { fireEvent.click(submit); });
    expect(create).toHaveBeenCalledWith("me@home.lan", "correct horse battery");
    expect(signedIn).toHaveBeenCalledOnce();
  });

  it("shows the server's refusal and stays on the step", async () => {
    vi.spyOn(setupApi, "createAccount").mockRejectedValue(new ApiError("An account already exists. Sign in instead.", 409));
    const signedIn = page();
    await toAccount();
    await type("Email", "me@home.lan");
    await type("Password", "correct horse battery");
    await type("Confirm password", "correct horse battery");
    await click("Create account and open Arrmada");
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText(/An account already exists/)).toBeTruthy();
    expect(signedIn).not.toHaveBeenCalled();
  });

  it("Skip to account jumps to the last step without confirming", async () => {
    page();
    await click("Skip to account");
    expect(await screen.findByText("Create your account")).toBeTruthy();
    expect(screen.queryByText("Skip to account")).toBeNull();
  });

  it("Back from the account returns to Indexers", async () => {
    page();
    await toAccount();
    await click("Back");
    expect(await screen.findByText("Where releases come from")).toBeTruthy();
  });

  it("starts the session again once when it ended mid-wizard (401), then carries on", async () => {
    const save = vi.spyOn(api, "setLibraryPaths")
      .mockRejectedValueOnce(new ApiError("sign in", 401))
      .mockResolvedValue(paths);
    page();
    await click("Skip this step"); // media
    await click("Continue"); // keys
    await click("Save and continue"); // folders
    expect(save).toHaveBeenCalledTimes(2);
    expect(startSession).toHaveBeenCalledTimes(2);
    expect(await screen.findByText("Where releases come from")).toBeTruthy();
  });

  it("says nothing was started when a setup session can't be opened", async () => {
    startSession.mockRejectedValue(new ApiError("Setup is only available from your home network.", 403));
    page();
    expect(await screen.findByText(/only available from your home network/)).toBeTruthy();
  });
});

describe("passwordHint", () => {
  it("only requires the minimum, and grades the rest", () => {
    expect(passwordHint("").tone).toBe("idle");
    expect(passwordHint("abc").tone).toBe("bad");
    expect(passwordHint("abcdefgh").tone).toBe("ok");
    expect(passwordHint("Abcdefgh1234!").tone).toBe("good");
  });
});
