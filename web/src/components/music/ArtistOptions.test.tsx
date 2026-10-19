// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ConfirmProvider, ToastProvider } from "../../ui";
import { musicApi, type ArtistOptions as Options, type ReleaseTypeChoices } from "../../lib/musicApi";
import { ArtistOptions } from "./ArtistOptions";

// MUS-14: an artist's options round trip. Saving sends exactly what was chosen and never
// wants anything by itself; "Want matching albums" asks first, showing the plan, and sends
// the counts the person saw.

const choices: ReleaseTypeChoices = {
  primary: ["Album", "EP", "Single", "Broadcast", "Other"],
  secondary: ["Compilation", "Soundtrack", "Live", "Remix"],
};
const base: Options = {
  monitor_new: "",
  release_types: null,
  effective_monitor_new: "all",
  effective_release_types: { primary: ["Album", "EP"], secondary: [] },
  default_monitor_new: "all",
  default_release_types: { primary: ["Album", "EP"], secondary: [] },
  quality_profile: "",
  apply: { want: [], unwant: [], singles: [] },
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const open = (onChanged = () => {}) =>
  render(
    <MemoryRouter>
      <ToastProvider>
        <ConfirmProvider>
          <ArtistOptions artistId={7} onChanged={onChanged} />
        </ConfirmProvider>
      </ToastProvider>
    </MemoryRouter>,
  );

describe("Artist options", () => {
  it("saves the new-release choice and the artist's own release types", async () => {
    vi.spyOn(musicApi, "artistOptions").mockResolvedValue({ options: base, choices });
    const saved: Options = {
      ...base,
      monitor_new: "none",
      release_types: { primary: ["Album", "EP"], secondary: ["Live"] },
      effective_monitor_new: "none",
      effective_release_types: { primary: ["Album", "EP"], secondary: ["Live"] },
      apply: { want: [{ id: 3, title: "I Might Be Wrong", year: 2001 }], unwant: [], singles: [] },
    };
    const set = vi.spyOn(musicApi, "setArtistOptions").mockResolvedValue({ options: saved, choices, refreshed: true });
    const changed = vi.fn();
    open(changed);

    const save = await screen.findByRole("button", { name: "Save options" });
    expect((save as HTMLButtonElement).disabled).toBe(true); // nothing changed yet
    await act(async () => {
      fireEvent.change(screen.getByLabelText("New releases"), { target: { value: "none" } });
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("switch", { name: /use the library default/ }));
    });
    await act(async () => {
      fireEvent.click(screen.getByLabelText("Live"));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Save options" }));
    });
    expect(set).toHaveBeenCalledWith(7, { monitor_new: "none", release_types: { primary: ["Album", "EP"], secondary: ["Live"] } });
    expect(changed).toHaveBeenCalled(); // the types changed: the artist page reloads its albums
    expect(await screen.findByText(/Applying the types would want 1 album these types allow/)).toBeTruthy();
  });

  it("asks before wanting matching albums, then sends the counts it showed", async () => {
    const plan = { want: [{ id: 3, title: "I Might Be Wrong", year: 2001 }], unwant: [{ id: 4, title: "Drill EP" }], singles: [] };
    vi.spyOn(musicApi, "artistOptions").mockResolvedValue({ options: { ...base, apply: plan }, choices });
    const apply = vi.spyOn(musicApi, "applyArtistOptions").mockResolvedValue({ applied: plan });
    open();
    const want = await screen.findByRole("button", { name: "Want matching albums…" });
    await act(async () => {
      fireEvent.click(want);
    });
    expect(apply).not.toHaveBeenCalled(); // nothing happens before the confirm
    expect(screen.getByText(/Wanted: I Might Be Wrong \(2001\)/)).toBeTruthy();
    expect(screen.getByText(/No longer wanted: Drill EP/)).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    });
    expect(apply).toHaveBeenCalledWith(7, plan);
  });
});
