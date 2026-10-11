// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useSearchParamState } from "./useSearchState";
import { remember, scrollAction } from "./useScrollMemory";

afterEach(() => { cleanup(); vi.useRealTimers(); });

let setFilter: (v: string) => void = () => {};
let setQ: (v: string) => void = () => {};

function Probe() {
  const [filter, sf] = useSearchParamState<string>("filter", "all", { allowed: ["all", "wanted", "missing"] });
  const [q, sq] = useSearchParamState<string>("q", "", { debounceMs: 300 });
  setFilter = sf;
  setQ = sq;
  const loc = useLocation();
  return <div><span data-testid="filter">{filter}</span><span data-testid="q">{q}</span><span data-testid="search">{loc.search}</span></div>;
}

const page = (url: string) => render(<MemoryRouter initialEntries={[url]}><Probe /></MemoryRouter>);
const text = (id: string) => screen.getByTestId(id).textContent;

describe("useSearchParamState", () => {
  it("reads the address, and leaves the default out of it", () => {
    page("/movies?filter=wanted&q=dune");
    expect(text("filter")).toBe("wanted");
    expect(text("q")).toBe("dune");
    act(() => setFilter("all"));
    expect(text("search")).toBe("?q=dune");
  });

  it("falls back to the default for a value the page doesn't offer", () => {
    page("/movies?filter=bogus");
    expect(text("filter")).toBe("all");
  });

  it("updates the value at once but writes a search to the address only once typing pauses", () => {
    vi.useFakeTimers();
    page("/movies?filter=wanted");
    act(() => setQ("du"));
    act(() => setQ("dune"));
    expect(text("q")).toBe("dune");
    expect(text("search")).toBe("?filter=wanted");
    // A filter changed meanwhile survives the delayed write.
    act(() => setFilter("missing"));
    act(() => { vi.advanceTimersByTime(300); });
    expect(text("search")).toBe("?filter=missing&q=dune");
  });
});

describe("scroll memory", () => {
  it("restores on Back/Forward, opens new pages at the top, and leaves a filter edit alone", () => {
    expect(scrollAction("POP", true)).toBe("restore");
    expect(scrollAction("PUSH", false)).toBe("top");
    expect(scrollAction("REPLACE", true)).toBe("top");
    expect(scrollAction("REPLACE", false)).toBe("keep");
  });

  it("keeps the newest hundred entries", () => {
    let saved: Record<string, number> = {};
    for (let i = 0; i < 105; i++) saved = remember(saved, `k${i}`, i * 10);
    expect(Object.keys(saved)).toHaveLength(100);
    expect(saved.k0).toBeUndefined();
    expect(saved.k104).toBe(1040);
    saved = remember(saved, "k5", 7); // revisited: moves to the newest end
    saved = remember(saved, "k200", 0);
    expect(saved.k5).toBe(7);
  });
});
