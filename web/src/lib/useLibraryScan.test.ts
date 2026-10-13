// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { api, ApiError, type Job, type JobStatus } from "./api";
import { resetLiveForTests } from "./live";
import { scanLine, unmatchedOf, useLibraryScan, type LibraryScan } from "./useLibraryScan";

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

  it("asks nothing for a row without a scan", () => {
    const jobs = vi.spyOn(api, "jobs").mockResolvedValue([]);
    renderHook(() => useLibraryScan("", vi.fn()));
    expect(jobs).not.toHaveBeenCalled();
  });
});

describe("scanLine", () => {
  const scan = (over: Partial<LibraryScan>): LibraryScan => ({ start: async () => {}, running: false, job: null, note: null, ...over });

  it("says a scan is waiting its turn behind another", () => {
    expect(scanLine("TV Shows", scan({ running: true, job: job("queued") }))?.text).toBe("TV Shows: waiting for another library scan to finish…");
  });

  it("shows a failed scan's error, and nothing before any scan", () => {
    expect(scanLine("Music", scan({ job: job("failed", { error: "music folder isn't mounted" }) }))).toEqual({ text: "Scan failed: music folder isn't mounted", tone: "error" });
    expect(scanLine("Music", scan({}))).toBeNull();
  });
});
