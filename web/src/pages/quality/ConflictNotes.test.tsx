// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ConflictNotes } from "./ConflictNotes";

afterEach(cleanup);

describe("ConflictNotes", () => {
  it("shows a row's warnings under it, and nothing once they're cleared", () => {
    const msg = "Avoiding HDR10 also avoids HDR10+ (it's HDR10 underneath).";
    const { rerender, container } = render(<ConflictNotes conflicts={[{ row: "hdr", msg }, { row: "codec", msg: "Every codec is set to Avoid." }]} row="hdr" />);
    expect(screen.getByText(msg)).toBeTruthy();
    expect(screen.queryByText("Every codec is set to Avoid.")).toBeNull();
    rerender(<ConflictNotes conflicts={[]} row="hdr" />);
    expect(container.textContent).toBe("");
  });
});
