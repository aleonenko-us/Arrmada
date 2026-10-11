// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook } from "@testing-library/react";
import type { MediaRequest } from "./api";
import { resetLiveForTests } from "./live";
import { FEED_IDLE_MS, FEED_MOVING_MS, feedMoving, useRequestsFeed } from "./useRequestsFeed";

class FakeSocket {
  static all: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(public url: string) { FakeSocket.all.push(this); }
  close() {}
  send(topic: string) { this.onmessage?.({ data: JSON.stringify({ topic, data: { id: 1, status: "approved", media_type: "movie" } }) }); }
}

let visibility: DocumentVisibilityState = "visible";
beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  resetLiveForTests();
});
afterEach(() => {
  cleanup();
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const rq = (stage: string) => ({ id: 1, tracking: { stage } }) as unknown as MediaRequest;

describe("useRequestsFeed", () => {
  it("is quick only while something moves on its own", () => {
    expect(feedMoving([rq("downloading")])).toBe(true);
    expect(feedMoving([rq("adding")])).toBe(true);
    expect(feedMoving([rq("searching"), rq("available")])).toBe(false);
    expect(feedMoving(null)).toBe(false);
  });

  it("refreshes on the viewer's own request event, and otherwise once a minute", async () => {
    const load = vi.fn();
    renderHook(() => useRequestsFeed(load, [rq("searching")]));
    FakeSocket.all[0].onopen?.();
    FakeSocket.all[0].send("user.7.request.updated");
    await vi.advanceTimersByTimeAsync(600);
    expect(load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(FEED_IDLE_MS - 1000);
    expect(load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("polls every 10 s while downloading, and never in a hidden tab", async () => {
    const load = vi.fn();
    renderHook(() => useRequestsFeed(load, [rq("downloading")]));
    await vi.advanceTimersByTimeAsync(FEED_MOVING_MS);
    expect(load).toHaveBeenCalledTimes(1);
    visibility = "hidden";
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(5 * FEED_IDLE_MS);
    expect(load).toHaveBeenCalledTimes(1);
  });
});
