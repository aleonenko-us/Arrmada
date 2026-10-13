// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { systemApi, type LogPage } from "../lib/systemApi";
import { ToastProvider } from "../ui";
import { Logs } from "./Logs";

// CFG-30/31: Live mode asks only for what's new and appends it; a new server run starts
// the view over; the debug switch is timed.

const line = (seq: number, msg: string, module?: string) => ({ seq, time_ms: 1_700_000_000_000 + seq, level: "INFO", msg, module });
const page = (p: Partial<LogPage>): LogPage => ({ entries: [], last_seq: 0, run: 1, modules: ["convert", "indexer"], capacity: 50000, ...p });

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.spyOn(systemApi, "logLevel").mockResolvedValue({ level: "info", env_level: "info", max_minutes: 240 });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const show = () => render(<MemoryRouter><ToastProvider><Logs /></ToastProvider></MemoryRouter>);

describe("Logs", () => {
  it("polls with the last cursor and appends only the new lines", async () => {
    const logs = vi.spyOn(systemApi, "logs")
      .mockResolvedValueOnce(page({ entries: [line(1, "first"), line(2, "second", "convert")], last_seq: 2 }))
      .mockResolvedValueOnce(page({ entries: [line(3, "third")], last_seq: 3 }))
      .mockResolvedValue(page({ entries: [], last_seq: 3 }));
    show();
    expect(await screen.findByText("second")).toBeTruthy();
    expect(screen.getByText("[convert]")).toBeTruthy();
    expect(logs.mock.calls[0][0].after_seq).toBeUndefined();

    await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
    expect(logs.mock.calls[1][0].after_seq).toBe(2);
    expect(await screen.findByText("third")).toBeTruthy();
    expect(screen.getByText("first")).toBeTruthy(); // appended, not replaced
    expect(screen.getByText(/of the last 50,000 kept in memory/)).toBeTruthy();
  });

  it("starts over when the server has restarted", async () => {
    const logs = vi.spyOn(systemApi, "logs")
      .mockResolvedValueOnce(page({ entries: [line(1, "before restart")], last_seq: 1 }))
      .mockResolvedValueOnce(page({ entries: [line(5, "restored")], last_seq: 5, run: 2 }))
      .mockResolvedValue(page({ entries: [line(5, "after restart")], last_seq: 5, run: 2 }));
    show();
    expect(await screen.findByText("before restart")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
    expect(await screen.findByText("after restart")).toBeTruthy();
    expect(screen.queryByText("before restart")).toBeNull();
    expect(logs.mock.calls[2][0].after_seq).toBeUndefined(); // a full load, not an increment
  });

  it("turns debug on for 30 minutes and offers the way back", async () => {
    vi.spyOn(systemApi, "logs").mockResolvedValue(page({}));
    const until = new Date(Date.now() + 30 * 60_000).toISOString();
    const set = vi.spyOn(systemApi, "setLogLevel").mockResolvedValue({ level: "debug", env_level: "info", until, max_minutes: 240 });
    show();
    const button = await screen.findByText("Debug for 30 min");
    await act(async () => { fireEvent.click(button); });
    expect(set).toHaveBeenCalledWith("debug", 30);
    expect(await screen.findByText(/Debug on · 30:00 left|Debug on · 29:5\d left/)).toBeTruthy();
    expect(screen.getByText("Back to normal")).toBeTruthy();
    expect(screen.getByRole("button", { name: "debug" }).getAttribute("aria-pressed")).toBe("true");
  });
});
