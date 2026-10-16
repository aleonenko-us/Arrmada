// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { useState } from "react";
import { foldersApi, type LibraryFolders } from "../lib/foldersApi";
import { clearQueryCache } from "../lib/query";
import { FolderSelect } from "./FolderSelect";

// FOLDERS: the add and approve dialogs show a folder picker only when the library has more
// than one folder — with one, nothing renders and the dialogs look as they always did.

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  clearQueryCache();
});

const one: LibraryFolders = {
  movie: { multi: false, folders: [{ id: 0, media: "movie", path: "/storage/movies", label: "Movies", rule: "", position: 0, default: true, titles: 3 }] },
  series: { multi: false, folders: [{ id: 0, media: "series", path: "/storage/tv", label: "TV", rule: "", position: 0, default: true, titles: 1 }] },
};
const two: LibraryFolders = {
  ...one,
  movie: { multi: true, folders: [...one.movie.folders, { id: 4, media: "movie", path: "/storage/kids", label: "Kids", rule: "kids", position: 1, default: false, titles: 0 }] },
};

function Harness({ tmdbId, inline }: { tmdbId?: number; inline?: boolean }) {
  const [v, setV] = useState<number | undefined>(undefined);
  return (
    <>
      <FolderSelect media="movie" tmdbId={tmdbId} value={v} onChange={setV} inline={inline} />
      <output data-testid="value">{v === undefined ? "rules" : String(v)}</output>
    </>
  );
}

describe("FolderSelect", () => {
  it("renders nothing while the library has one folder, and asks for no suggestion", async () => {
    vi.spyOn(foldersApi, "list").mockResolvedValue(one);
    const suggest = vi.spyOn(foldersApi, "suggest").mockResolvedValue(0);
    await act(async () => { render(<Harness tmdbId={862} />); });
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(suggest).not.toHaveBeenCalled();
    expect(screen.getByTestId("value").textContent).toBe("rules");
  });

  it("offers every folder and preselects the one the rules pick", async () => {
    vi.spyOn(foldersApi, "list").mockResolvedValue(two);
    const suggest = vi.spyOn(foldersApi, "suggest").mockResolvedValue(4);
    await act(async () => { render(<Harness tmdbId={862} />); });
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    expect([...select.options].map((o) => o.text)).toEqual(["Movies (default)", "Kids"]);
    expect(suggest).toHaveBeenCalledWith("movie", 862);
    expect(select.value).toBe("4");
    expect(screen.getByTestId("value").textContent).toBe("4");
  });

  it("starts from the rules on a dialog that adds from a list", async () => {
    vi.spyOn(foldersApi, "list").mockResolvedValue(two);
    await act(async () => { render(<Harness inline />); });
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    expect(select.options[0].text).toBe("By the folder rules");
    expect(screen.getByTestId("value").textContent).toBe("rules");
  });
});
