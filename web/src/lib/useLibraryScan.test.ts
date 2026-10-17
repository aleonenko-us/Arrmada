// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { api, ApiError, type Job, type JobStatus } from "./api";
import { resetLiveForTests } from "./live";
import { foundLine, scanLine, unmatchedOf, useLibraryScan, type LibraryScan } from "./useLibraryScan";

// CFG-25: Settings → Library follows a scan's job — live progress, then what it found —
// instead of reloading after a fixed five seconds.

// No live socket in these tests: the follower polls the job instead.
class FakeSocket {
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

function job(status: JobStatus, extra: Partial<Job> = {}): Job {
  return {
    id: 5, kind: "movie.scan", target: "all", trigger: "user:1", status, progress: 0,
    message: "", error: "", created_at: null, started_at: null, finished_at: null, ...extra,
  };
}

beforeEach(() => {
  vi.stubGlobal("WebSocket", FakeSocket);
  resetLiveForTests();
});
afterEach(() => {
  cleanup();
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("useLibraryScan", () => {
  it("follows the running scan when Scan is pressed twice, and says what it found", async () => {
    vi.spyOn(api, "jobs").mockResolvedValue([]);
    vi.spyOn(api, "job").mockResolvedValue(job("succeeded", { progress: 1, message: "Added 3 movies; 1 folder needs review", result: { imported: 3, skipped: 0, unmatched: 1 } }));
    const begin = vi.fn().mockRejectedValue(new ApiError("a library scan is already running", 409, { job_id: 5 }));
    const onFinished = vi.fn();
    const { result } = renderHook(() => useLibraryScan("movie.scan", begin, onFinished));
    await act(async () => { await result.current.start(); });
    await waitFor(() => expect(onFinished).toHaveBeenCalledOnce());
    expect(api.job).toHaveBeenCalledWith(5);
    expect(result.current.note).toEqual({ text: "Already scanning — showing its progress.", error: false });
    expect(result.current.running).toBe(false);
    expect(scanLine("Movies", result.current)).toEqual({ text: "Done: Added 3 movies; 1 folder needs review", tone: "good" });
    expect(unmatchedOf(result.current.job)).toBe(1);
  });

  it("picks up a scan that was already running and shows its progress", async () => {
    const running = job("running", { id: 9, progress: 0.25, message: "Scanning 100 / 400 folders" });
    vi.spyOn(api, "jobs").mockResolvedValue([running]);
    vi.spyOn(api, "job").mockResolvedValue(running);
    const { result } = renderHook(() => useLibraryScan("movie.scan", vi.fn()));
    await waitFor(() => expect(result.current.job?.id).toBe(9));
    expect(result.current.running).toBe(true);
    expect(scanLine("Movies", result.current)).toEqual({ text: "Movies: Scanning 100 / 400 folders · 25%", tone: "dim" });
  });

  it("stops the scan it's following (a big library's can run for hours)", async () => {
    const running = job("running", { id: 9, progress: 0.25, message: "Scanning 100 / 400 folders" });
    vi.spyOn(api, "jobs").mockResolvedValue([running]);
    vi.spyOn(api, "job").mockResolvedValue(running);
    const { systemApi } = await import("./systemApi");
    const cancel = vi.spyOn(systemApi, "cancelJob").mockResolvedValue({ status: "cancelling", job_id: 9 });
    const { result } = renderHook(() => useLibraryScan("movie.scan", vi.fn()));
    await waitFor(() => expect(result.current.job?.id).toBe(9));
    await act(async () => { await result.current.stop(); });
    expect(cancel).toHaveBeenCalledWith(9);
    expect(result.current.note).toBeNull();
  });

  // GitHub #3: each extra folder follows only its own scan, and the main row only scans of
  // the whole library.
  it("follows only scans of its target", async () => {
    const other = job("running", { id: 9, target: "folder:4", message: "Scanning 1 / 3 show folders" });
    const mine = job("running", { id: 10, target: "folder:3", message: "Scanning 2 / 5 folders", progress: 0.4 });
    const jobs = vi.spyOn(api, "jobs").mockResolvedValue([other, mine]);
    vi.spyOn(api, "job").mockResolvedValue(mine);
    const { result } = renderHook(() => useLibraryScan("movie.scan", vi.fn(), undefined, "folder:3"));
    await waitFor(() => expect(result.current.job?.id).toBe(10));
    expect(jobs).toHaveBeenCalledWith({ kind: "movie.scan", target: "folder:3", limit: 5 });

    vi.spyOn(api, "jobs").mockResolvedValue([other]);
    const whole = renderHook(() => useLibraryScan("movie.scan", vi.fn(), undefined, "all"));
    await waitFor(() => expect(api.jobs).toHaveBeenCalledWith({ kind: "movie.scan", target: "all", limit: 5 }));
    expect(whole.result.current.job).toBeNull();
    expect(whole.result.current.running).toBe(false);
  });

  it("asks nothing for a row without a scan", () => {
    const jobs = vi.spyOn(api, "jobs").mockResolvedValue([]);
    renderHook(() => useLibraryScan("", vi.fn()));
    expect(jobs).not.toHaveBeenCalled();
  });
});

describe("foundLine", () => {
  it("counts what a folder's scan added and what needs review", () => {
    expect(foundLine(job("succeeded", { result: { imported: 3, attached: 1, skipped: 0, unmatched: 2 } }))).toBe("Found 4 titles; 2 need review");
    expect(foundLine(job("succeeded", { result: { imported: 1, skipped: 0, unmatched: 1 } }))).toBe("Found 1 title; 1 needs review");
    expect(foundLine(job("succeeded", { result: { imported: 0, skipped: 2, unmatched: 0 } }))).toBe("Found 0 titles.");
  });
});

describe("scanLine", () => {
  const scan = (over: Partial<LibraryScan>): LibraryScan => ({ start: async () => {}, stop: async () => {}, running: false, job: null, note: null, ...over });

  it("says a scan is waiting its turn behind another", () => {
    expect(scanLine("TV Shows", scan({ running: true, job: job("queued") }))?.text).toBe("TV Shows: waiting for another library scan to finish…");
  });

  it("shows a failed scan's error, and nothing before any scan", () => {
    expect(scanLine("Music", scan({ job: job("failed", { error: "music folder isn't mounted" }) }))).toEqual({ text: "Scan failed: music folder isn't mounted", tone: "error" });
    expect(scanLine("Music", scan({}))).toBeNull();
  });

  it("says a stopped scan stopped", () => {
    expect(scanLine("TV Shows", scan({ job: job("cancelled") }))).toEqual({ text: "Scan stopped.", tone: "dim" });
  });
});
