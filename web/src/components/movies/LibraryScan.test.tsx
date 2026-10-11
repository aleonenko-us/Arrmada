// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ScanStatus, scanProgressLabel, type MovieScan } from "./LibraryScan";
import { LINKS } from "../../lib/links";
import type { Job } from "../../lib/api";

// MOV-20: a scan shows live progress while it runs, then a summary that links to the
// folders needing review.

const job = (over: Partial<Job> = {}): Job => ({
  id: 7, kind: "movie.scan", target: "all", trigger: "user:1", status: "running", progress: 0.26,
  message: "Scanning 312 / 1,204 folders", error: "", created_at: null, started_at: null, finished_at: null, ...over,
});
const scan = (over: Partial<MovieScan> = {}): MovieScan => ({
  start: async () => {}, running: false, job: null, summary: null, dismiss: () => {}, ...over,
});
const show = (s: MovieScan) => render(<MemoryRouter><ScanStatus scan={s} /></MemoryRouter>);

afterEach(cleanup);

describe("scanProgressLabel", () => {
  it("is the job's progress while the scan runs, and nothing otherwise", () => {
    expect(scanProgressLabel(scan({ running: true, job: job() }))).toBe("Scanning 312 / 1,204 folders · 26%");
    expect(scanProgressLabel(scan({ running: true, job: null }))).toBe("Scanning…");
    expect(scanProgressLabel(scan({ running: true, job: job({ status: "queued", progress: 0, message: "" }) }))).toMatch(/waiting for another library scan/);
    expect(scanProgressLabel(scan())).toBeUndefined();
  });
});

describe("ScanStatus", () => {
  it("shows nothing while the scan runs (the toolbar chip has it)", () => {
    const { container } = show(scan({ running: true, job: job(), summary: { imported: 1, skipped: 0, unmatched: 0, attached: 0, duplicates: 0 } }));
    expect(container.textContent).toBe("");
  });

  it("sums up a finished scan and links to the folders that need review", () => {
    const dismiss = vi.fn();
    show(scan({ summary: { imported: 812, skipped: 3, unmatched: 9, attached: 14, duplicates: 0 }, dismiss }));
    expect(screen.getByText("812 added · 14 attached")).toBeTruthy();
    const link = screen.getByRole("link", { name: /9 need review/ });
    expect(link.getAttribute("href")).toBe(LINKS.libraryReview);
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(dismiss).toHaveBeenCalled();
  });

  it("has no review link when every folder matched, and nothing when idle", () => {
    show(scan({ summary: { imported: 2, skipped: 0, unmatched: 0, attached: 0, duplicates: 1 } }));
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByText(/1 left alone/)).toBeTruthy();
    cleanup();
    const { container } = show(scan());
    expect(container.textContent).toBe("");
  });
});
