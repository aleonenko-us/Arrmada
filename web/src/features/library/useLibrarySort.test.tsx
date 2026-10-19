// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { useLibrarySort } from "./useLibrarySort";
import type { LibraryConfig } from "./types";

interface Row { id: number; title: string; year?: number }

const config: LibraryConfig<Row> = {
  noun: { one: "film", many: "films" },
  storageKey: "films",
  detailPath: () => "/",
  filters: [{ key: "all", label: "All" }],
  searchText: (r) => r.title,
  compare: (a, b) => a.title.localeCompare(b.title),
  columns: [
    { key: "year", label: "Year", sort: (r) => r.year, cell: () => null },
    { key: "codec", label: "Codec", sort: () => undefined, cell: () => null },
  ],
  tableMinWidth: 600,
  card: { meta: () => null, status: () => ({ label: "", color: "" }) },
  sorts: [{ key: "title", label: "Title" }, { key: "year", label: "Year", dir: "desc", value: (r) => r.year }],
};

afterEach(() => { cleanup(); localStorage.clear(); });

describe("useLibrarySort", () => {
  it("carries the old grid sort over, saves it and removes the old keys", () => {
    localStorage.setItem("films.grid.sort", "year");
    localStorage.setItem("films.table.sort", "codec:desc");
    const { result } = renderHook(() => useLibrarySort(config));
    expect(result.current.state).toEqual({ key: "year", dir: "desc" });
    expect(localStorage.getItem("films.sort")).toBe("year:desc");
    expect(localStorage.getItem("films.grid.sort")).toBeNull();
    expect(localStorage.getItem("films.table.sort")).toBeNull();
    // The next visit reads the same order.
    cleanup();
    const again = renderHook(() => useLibrarySort(config));
    expect(again.result.current.state).toEqual({ key: "year", dir: "desc" });
  });

  it("starts a grid user at Title rather than an old table sort", () => {
    localStorage.setItem("films.table.sort", "codec:desc");
    const { result } = renderHook(() => useLibrarySort(config));
    expect(result.current.state).toEqual({ key: "title", dir: "asc" });
  });

  it("a header click reverses the same column; a menu pick starts it its own way", () => {
    const { result } = renderHook(() => useLibrarySort(config));
    act(() => result.current.onColumn("year"));
    expect(result.current.state).toEqual({ key: "year", dir: "desc" });
    act(() => result.current.onColumn("year"));
    expect(result.current.state).toEqual({ key: "year", dir: "asc" });
    act(() => result.current.onPick("year"));
    expect(result.current.state).toEqual({ key: "year", dir: "desc" });
    act(() => result.current.reverse());
    expect(localStorage.getItem("films.sort")).toBe("year:asc");
  });

  it("an unknown remembered key falls back to Title A→Z", () => {
    localStorage.setItem("films.sort", "gone:desc");
    const { result } = renderHook(() => useLibrarySort(config));
    expect(result.current.state).toEqual({ key: "title", dir: "asc" });
  });
});
