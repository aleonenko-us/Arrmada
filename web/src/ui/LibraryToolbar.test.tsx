// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { LibraryToolbar } from "./LibraryToolbar";
import { FilterBar } from "./FilterBar";

afterEach(cleanup);

describe("FilterBar", () => {
  it("shows every pill's count and says which noun it searches", () => {
    const onFilter = vi.fn();
    const onQuery = vi.fn();
    render(<FilterBar filters={[{ key: "all", label: "All", count: 12 }, { key: "wanted", label: "Wanted", count: 3 }]} active="all" onFilter={onFilter} query="" onQuery={onQuery} noun="artists" />);
    expect(screen.getByRole("button", { name: "All 12" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Wanted 3" }));
    expect(onFilter).toHaveBeenCalledWith("wanted");
    const box = screen.getByPlaceholderText("Search artists…");
    fireEvent.change(box, { target: { value: "dune" } });
    expect(onQuery).toHaveBeenCalledWith("dune");
  });
});

describe("LibraryToolbar", () => {
  it("keeps the tools in a menu and a running sweep's progress in sight while it's closed", () => {
    const scan = vi.fn();
    render(
      <LibraryToolbar
        count={7}
        tools={[
          { label: "Scan library", onClick: scan },
          { label: "Search missing (4)", onClick: () => {}, busy: true, progress: "Searching… 2/5" },
          { label: "Re-match", onClick: () => {}, hidden: true },
        ]}
        primary={{ label: "+ Add book", onClick: () => {} }}
      />,
    );
    expect(screen.getByText("7 in library")).toBeTruthy();
    expect(screen.getByText("Searching… 2/5")).toBeTruthy(); // the chip, menu closed
    expect(screen.queryByRole("menuitem")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Library tools" }));
    const items = screen.getAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual(["Scan library", "Searching… 2/5"]);
    expect((items[1] as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(items[0]);
    expect(scan).toHaveBeenCalled();
  });

  it("shows everyday actions as buttons, pickers as selects, and a count on the tools", () => {
    const sweep = vi.fn();
    const view = vi.fn();
    render(
      <LibraryToolbar
        count={3}
        pickers={[{ label: "View", value: "a", options: [{ value: "a", label: "Authors" }, { value: "b", label: "Books" }], onChange: view }]}
        tools={[{ label: "Scan library", onClick: () => {} }]}
        toolsBadge={2}
        actions={[{ label: "Search missing (4)", onClick: sweep }, { label: "Busy one", onClick: () => {}, busy: true, busyLabel: "Searching… 1/4" }]}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Search missing (4)" }));
    expect(sweep).toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "Searching… 1/4" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("View"), { target: { value: "b" } });
    expect(view).toHaveBeenCalledWith("b");
    // The badge is visual; the button's name stays "Library tools".
    expect(screen.getByRole("button", { name: "Library tools" }).textContent).toContain("2");
  });

  it("hides the count until the list has loaded", () => {
    render(<LibraryToolbar count={null} />);
    expect(screen.queryByText(/in library/)).toBeNull();
  });
});
