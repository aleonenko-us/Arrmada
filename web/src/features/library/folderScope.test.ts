import { describe, expect, it } from "vitest";
import type { LibraryFolderSet } from "../../lib/foldersApi";
import { folderOptions, inFolder, pickedFolder } from "./folderScope";

const one: LibraryFolderSet = { multi: false, folders: [{ id: 0, media: "movie", path: "/media/movies", label: "Movies", rule: "", position: 0, default: true, titles: 3 }] };
const two: LibraryFolderSet = { multi: true, folders: [...one.folders, { id: 3, media: "movie", path: "/media/cartoons", label: "Cartoons", rule: "animation", position: 1, default: false, titles: 1 }] };

describe("folder filter", () => {
  it("picks the folder ?folder= names only when there are several", () => {
    expect(pickedFolder(two, "3")?.label).toBe("Cartoons");
    expect(pickedFolder(two, "0")?.label).toBe("Movies");
    expect(pickedFolder(two, "")).toBeUndefined();
    expect(pickedFolder(two, "9")).toBeUndefined(); // removed since: all folders
    expect(pickedFolder(one, "0")).toBeUndefined(); // one folder: no filter at all
    expect(pickedFolder(undefined, "3")).toBeUndefined(); // not loaded yet
  });
  it("counts a title without a folder id as the default folder's", () => {
    expect(inFolder({}, 0)).toBe(true);
    expect(inFolder({}, 3)).toBe(false);
    expect(inFolder({ library_folder_id: 3 }, 3)).toBe(true);
    expect(inFolder({ library_folder_id: 3 }, 0)).toBe(false);
  });
  it("offers All folders, then each folder in order", () => {
    expect(folderOptions(two)).toEqual([
      { value: "", label: "All folders" },
      { value: "0", label: "Movies (default)" },
      { value: "3", label: "Cartoons" },
    ]);
  });
});
