// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { clearQueryCache } from "../../lib/query";
import { ConfirmProvider } from "../../ui";
import { profileApi, type ProfileList } from "../subtitles/profileApi";
import { ProfileSelect, TitleProfilePicker } from "../subtitles/ProfileSelect";
import { unsavedMessage } from "../../lib/useUnsavedGuard";
import { SubtitleProfiles } from "./SubtitleProfiles";

// SUB-33: the profile editor sets each language's variant, SDH taste and "only when the
// audio differs", and the Library's per-title picker only appears once there's a choice.

vi.mock("../../lib/languages", async (orig) => ({
  ...(await orig<typeof import("../../lib/languages")>()),
  useLanguages: () => [
    { code: "en", name: "English", regional: false },
    { code: "ja", name: "Japanese", regional: false },
  ],
}));

const defaultOnly: ProfileList = {
  profiles: [{ id: 1, name: "Default", is_default: true, titles: 0, items: [{ lang: "en", variant: "full", sdh: "any", only_if_audio_differs: false }] }],
  overrides: [],
};
const withAnime: ProfileList = {
  profiles: [
    ...defaultOnly.profiles,
    { id: 2, name: "Anime", is_default: false, titles: 1, items: [{ lang: "ja", variant: "full", sdh: "any", only_if_audio_differs: false }] },
  ],
  overrides: [{ kind: "series", id: 7, profile_id: 2, title: "Frieren" }],
};

beforeEach(() => clearQueryCache());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const editor = () => render(<MemoryRouter><ConfirmProvider><SubtitleProfiles version="en" /></ConfirmProvider></MemoryRouter>);

describe("Subtitle language profiles", () => {
  it("saves a language's variant and audio option on the default profile", async () => {
    vi.spyOn(profileApi, "list").mockResolvedValue(defaultOnly);
    const update = vi.spyOn(profileApi, "update").mockResolvedValue(defaultOnly.profiles[0]);
    editor();
    const variant = await screen.findByLabelText("English: which subtitle");
    await act(async () => { fireEvent.change(variant, { target: { value: "both" } }); });
    expect(update).toHaveBeenLastCalledWith(1, { items: [{ lang: "en", variant: "both", sdh: "any", only_if_audio_differs: false }] });
    await act(async () => { fireEvent.click(screen.getByLabelText("Only when the audio is another language")); });
    expect(update).toHaveBeenLastCalledWith(1, { items: [{ lang: "en", variant: "full", sdh: "any", only_if_audio_differs: true }] });
  });

  it("lists who uses a profile and puts a title back on Default", async () => {
    vi.spyOn(profileApi, "list").mockResolvedValue(withAnime);
    const set = vi.spyOn(profileApi, "setOverride").mockResolvedValue(undefined);
    editor();
    expect(await screen.findByText("Frieren")).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByLabelText("Put Frieren back on Default")); });
    expect(set).toHaveBeenCalledWith("series", 7, 0);
  });

  it("the Library picker hides until a second profile exists, then pins the title", async () => {
    vi.spyOn(profileApi, "list").mockResolvedValue(defaultOnly);
    const { unmount } = render(<ProfileSelect kind="movie" id={3} title="Heat" onChanged={() => {}} />);
    await act(async () => {});
    expect(screen.queryByLabelText("Subtitle profile for Heat")).toBeNull();
    unmount();

    clearQueryCache();
    vi.spyOn(profileApi, "list").mockResolvedValue(withAnime);
    const set = vi.spyOn(profileApi, "setOverride").mockResolvedValue(undefined);
    const changed = vi.fn();
    render(<ProfileSelect kind="movie" id={3} title="Heat" onChanged={changed} />);
    const sel = await screen.findByLabelText("Subtitle profile for Heat");
    await act(async () => { fireEvent.change(sel, { target: { value: "2" } }); });
    expect(set).toHaveBeenCalledWith("movie", 3, 2);
    expect(changed).toHaveBeenCalled();
  });

  // SUB-34: the Movie and Series pages' "Subtitles" selector.
  it("the detail-page picker shows the title's pin and moves it back to Default", async () => {
    vi.spyOn(profileApi, "list").mockResolvedValue(defaultOnly);
    const { unmount } = render(<TitleProfilePicker kind="series" id={7} title="Frieren" />);
    await act(async () => {});
    expect(screen.queryByLabelText("Subtitles")).toBeNull();
    unmount();

    clearQueryCache();
    vi.spyOn(profileApi, "list").mockResolvedValue(withAnime);
    const set = vi.spyOn(profileApi, "setOverride").mockResolvedValue(undefined);
    const changed = vi.fn();
    render(<TitleProfilePicker kind="series" id={7} title="Frieren" onChanged={changed} />);
    const sel = (await screen.findByLabelText("Subtitles")) as HTMLSelectElement;
    expect(sel.value).toBe("2"); // pinned to Anime
    await act(async () => { fireEvent.change(sel, { target: { value: "1" } }); });
    expect(set).toHaveBeenCalledWith("series", 7, 0); // Default = no pin
    expect(changed).toHaveBeenCalled();
  });

  it("a profile name typed but not saved guards leaving the page", async () => {
    vi.spyOn(profileApi, "list").mockResolvedValue(withAnime);
    const update = vi.spyOn(profileApi, "update").mockResolvedValue(withAnime.profiles[1]);
    editor();
    const input = await screen.findByLabelText("Profile name");
    await act(async () => { fireEvent.change(input, { target: { value: "Anime (JP)" } }); });
    expect(unsavedMessage()).toBe("Discard the profile's new name?");
    await act(async () => { fireEvent.blur(input); });
    expect(update).toHaveBeenCalledWith(2, { name: "Anime (JP)" });
    expect(unsavedMessage()).toBeNull();
  });
});
