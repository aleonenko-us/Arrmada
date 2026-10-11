// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { api, SIGNED_OUT_EVENT, type Attention, type AttentionCounts } from "./api";
import { badgeFor, useAttention, useAttentionPoll } from "./useAttention";
import { resetLiveForTests } from "./live";

// FE-28: the sidebar pills follow attention.changed on the tab's one socket, not a poll.

class FakeSocket {
  static all: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(public url: string) { FakeSocket.all.push(this); }
  close() {}
}

const zero: AttentionCounts = { requests: 0, reviews: 0, downloads: 0, imports: 0, searches: 0, health: 0, health_errors: 0, total: 0 };
const snapshot = (requests: number): Attention => ({ at: null, stale: false, counts: { ...zero, requests, total: requests }, groups: [], items: [] });

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  resetLiveForTests();
});
afterEach(() => {
  cleanup();
  window.dispatchEvent(new Event(SIGNED_OUT_EVENT)); // clears the shared snapshot
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("live Needs-you badges", () => {
  it("renders the pill from the snapshot and updates on attention.changed", async () => {
    const attention = vi.spyOn(api, "attention").mockResolvedValueOnce(snapshot(2)).mockResolvedValueOnce(snapshot(1));
    const { result } = renderHook(() => { useAttentionPoll(); return useAttention(); });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(badgeFor("requests", result.current?.counts)?.count).toBe(2);
    expect(document.title.startsWith("(2) ")).toBe(true);

    // A request approved elsewhere: the server says the counts moved.
    const sock = FakeSocket.all[0];
    act(() => sock.onopen?.());
    act(() => sock.onmessage?.({ data: JSON.stringify({ topic: "attention.changed", data: { total: 1 } }) }));
    await act(() => vi.advanceTimersByTimeAsync(400));
    expect(attention).toHaveBeenCalledTimes(2);
    expect(badgeFor("requests", result.current?.counts)?.count).toBe(1);
    expect(document.title.startsWith("(1) ")).toBe(true);
    // One socket for the shell.
    expect(FakeSocket.all).toHaveLength(1);
  });
});
