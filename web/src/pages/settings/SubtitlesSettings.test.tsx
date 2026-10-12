// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api, type APIKeyStatus, type SubtitleSettings } from "../../lib/api";
import { ConfirmProvider } from "../../ui";
import { SubtitlesSettings } from "./SubtitlesSettings";

// SUB-13: the OpenSubtitles card shows what the provider last said (not whether three
// fields are filled in), and admins edit the credentials right here; saving one tests the
// login straight away and the state line follows.

const role = vi.hoisted(() => ({ current: "admin" }));
vi.mock("../../lib/me", async (orig) => ({
  ...(await orig<typeof import("../../lib/me")>()),
  useMe: () => ({ user: { id: 1, username: "u", role: role.current }, setMetadataReady: () => {} }),
}));
vi.mock("../../lib/usePoll", () => ({ usePoll: () => {} }));

const settings = (over: Partial<SubtitleSettings> = {}): SubtitleSettings => ({
  movies_auto: true, series_auto: true, languages: ["en"], provider_ready: true, can_download: true,
  ai_ready: false, ai_backend: "", ai_note: "", quota_remaining: -1, quota_reset_at: 0, pending: 0, min_score: 60,
  provider_state: "unverified", provider_detail: "Not checked yet — Test signs in, or the next download will.",
  vip: false, allowed_downloads: -1, ...over,
});

const key = (id: string, over: Partial<APIKeyStatus> = {}): APIKeyStatus => ({
  id, label: id, purpose: "", help_url: "", steps: "", secret: id !== "opensubtitles_username",
  testable: id === "opensubtitles_api", tests_candidate: id === "opensubtitles_api", configured: true, source: "settings", hint: "…", env_set: false, ...over,
});
const keys = [key("opensubtitles_api"), key("opensubtitles_username"), key("opensubtitles_password")];

beforeEach(() => {
  role.current = "admin";
  vi.spyOn(api, "subtitleModels").mockResolvedValue({ binary_ready: true, ready: false, models: [] });
  vi.spyOn(api, "apiKeys").mockResolvedValue(keys);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const page = () => render(<MemoryRouter><ConfirmProvider><SubtitlesSettings /></ConfirmProvider></MemoryRouter>);

describe("Subtitles settings · OpenSubtitles", () => {
  it("says a rejected login plainly, and a new password re-tests and refreshes the state", async () => {
    const get = vi.spyOn(api, "subtitleSettings")
      .mockResolvedValueOnce(settings({ provider_state: "auth_failed", provider_detail: "OpenSubtitles login rejected (401)" }))
      .mockResolvedValue(settings({ provider_state: "ok", vip: false, quota_remaining: 14, allowed_downloads: 20 }));
    const set = vi.spyOn(api, "setAPIKey").mockResolvedValue(keys);
    const test = vi.spyOn(api, "testAPIKey").mockResolvedValue({ ok: true, detail: "OK: signed in as me, 14 downloads left today." });
    page();
    expect(await screen.findByText("login rejected")).toBeTruthy();
    expect(screen.getByText("OpenSubtitles login rejected (401)")).toBeTruthy();

    const input = await screen.findByLabelText("opensubtitles_password");
    fireEvent.change(input, { target: { value: "new-secret" } });
    await act(async () => { fireEvent.click(screen.getAllByText("Save")[2]); });
    expect(set).toHaveBeenCalledWith("opensubtitles_password", "new-secret");
    // The login is checked through the API key's Test, straight after the save.
    expect(test).toHaveBeenCalledWith("opensubtitles_api", undefined);
    expect(await screen.findByText("Logged in · VIP no · 14/20 downloads left today")).toBeTruthy();
    expect(get.mock.calls.length).toBeGreaterThan(1);
  });

  it("shows a spent quota with its reset, and managers get a pointer instead of the fields", async () => {
    role.current = "manager";
    vi.spyOn(api, "subtitleSettings").mockResolvedValue(settings({ provider_state: "quota_spent", quota_reset_at: 1_900_000_000 }));
    const list = vi.spyOn(api, "apiKeys");
    page();
    expect(await screen.findByText("quota used")).toBeTruthy();
    expect(screen.getByText(/^Daily quota used — resumes /)).toBeTruthy();
    expect(screen.queryByLabelText("opensubtitles_password")).toBeNull();
    expect(screen.getByText(/An admin enters the OpenSubtitles API key/)).toBeTruthy();
    expect(list).not.toHaveBeenCalled();
  });
});
