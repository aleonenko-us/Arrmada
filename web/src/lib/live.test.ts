// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { BACKOFF_MIN_MS, HIDDEN_CLOSE_MS, IDLE_CLOSE_MS, resetLiveForTests, subscribe, topicMatches, useLiveConnected, useLiveEvent } from "./live";
import { useLiveQuery, useLiveRefresh } from "./useLiveQuery";
import { SIGNED_OUT_EVENT } from "./api";
import { clearQueryCache } from "./query";

// A stand-in for the browser's WebSocket: tests open, message and drop it by hand.
class FakeSocket {
  static all: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  closed = false;
  constructor(public url: string) { FakeSocket.all.push(this); }
  close() { this.closed = true; }
  // test helpers
  open() { this.onopen?.(); }
  send(topic: string, data: unknown = null) { this.onmessage?.({ data: JSON.stringify({ topic, data }) }); }
  drop() { this.closed = true; this.onclose?.(); }
}
const live = () => FakeSocket.all.filter((s) => !s.closed);
const last = () => FakeSocket.all[FakeSocket.all.length - 1];

let visibility: DocumentVisibilityState = "visible";
const setVisibility = (v: DocumentVisibilityState) => {
  visibility = v;
  document.dispatchEvent(new Event("visibilitychange"));
};

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  resetLiveForTests();
  clearQueryCache();
});
afterEach(() => {
  cleanup();
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const flush = () => vi.advanceTimersByTimeAsync(0);

describe("topicMatches", () => {
  it("matches exact topics, one-segment and trailing wildcards", () => {
    expect(topicMatches("movie.*", "movie.downloaded")).toBe(true);
    expect(topicMatches("movie.*", "movie.search.done")).toBe(true);
    expect(topicMatches("movie.*", "movie")).toBe(false);
    expect(topicMatches("movie.*", "series.imported")).toBe(false);
    expect(topicMatches("*", "anything.at.all")).toBe(true);
    expect(topicMatches("*.imported", "book.imported")).toBe(true);
    expect(topicMatches("*.imported", "import.held")).toBe(false);
    expect(topicMatches("user.*.request.updated", "user.7.request.updated")).toBe(true);
    expect(topicMatches("user.*.request.updated", "user.7.request.updated.x")).toBe(false);
    expect(topicMatches("queue.progress", "queue.progress")).toBe(true);
    expect(topicMatches("queue.progress", "queue.changed")).toBe(false);
  });
});

describe("the shared socket", () => {
  it("opens one socket for every listener in the tab", () => {
    const a = vi.fn(), b = vi.fn();
    const offA = subscribe(["movie.*"], a);
    const offB = subscribe(["queue.progress"], b);
    expect(FakeSocket.all).toHaveLength(1);
    last().open();
    last().send("movie.refreshed", { id: 1 });
    last().send("queue.progress", []);
    last().send("server.heartbeat");
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    offA();
    offB();
  });

  it("closes a moment after the last listener leaves, not on a page change", async () => {
    const off = subscribe(["x"], () => {});
    off();
    const again = subscribe(["y"], () => {}); // the next page subscribes straight away
    await vi.advanceTimersByTimeAsync(IDLE_CLOSE_MS + 10);
    expect(FakeSocket.all).toHaveLength(1);
    expect(live()).toHaveLength(1);
    again();
    await vi.advanceTimersByTimeAsync(IDLE_CLOSE_MS + 10);
    expect(live()).toHaveLength(0);
  });

  it("reconnects with backoff after a drop", async () => {
    const off = subscribe(["x"], () => {});
    last().drop();
    await vi.advanceTimersByTimeAsync(BACKOFF_MIN_MS - 1);
    expect(FakeSocket.all).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeSocket.all).toHaveLength(2);
    last().drop(); // second failure waits twice as long
    await vi.advanceTimersByTimeAsync(BACKOFF_MIN_MS);
    expect(FakeSocket.all).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(BACKOFF_MIN_MS);
    expect(FakeSocket.all).toHaveLength(3);
    off();
  });

  it("closes on sign-out and doesn't retry", async () => {
    const off = subscribe(["x"], () => {});
    last().open();
    window.dispatchEvent(new Event(SIGNED_OUT_EVENT));
    expect(live()).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeSocket.all).toHaveLength(1);
    off();
  });

  it("closes in a tab hidden for a while and reopens when shown", async () => {
    const off = subscribe(["x"], () => {});
    last().open();
    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(HIDDEN_CLOSE_MS + 10);
    expect(live()).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(FakeSocket.all).toHaveLength(1); // no retries while hidden
    setVisibility("visible");
    expect(FakeSocket.all).toHaveLength(2);
    off();
  });

  it("reports the connection state", () => {
    const { result } = renderHook(() => useLiveConnected());
    expect(result.current).toBe(false);
    act(() => last().open());
    expect(result.current).toBe(true);
    act(() => last().drop());
    expect(result.current).toBe(false);
  });

  it("useLiveEvent follows the latest handler without resubscribing", () => {
    const seen: string[] = [];
    const { rerender } = renderHook(({ tag }) => useLiveEvent("a.*", (ev) => seen.push(`${tag}:${ev.topic}`)), { initialProps: { tag: "one" } });
    last().open();
    last().send("a.b");
    rerender({ tag: "two" });
    last().send("a.c");
    expect(seen).toEqual(["one:a.b", "two:a.c"]);
    expect(FakeSocket.all).toHaveLength(1);
  });
});

describe("useLiveQuery", () => {
  it("refetches on a matching topic, one request per burst", async () => {
    const fetcher = vi.fn(() => Promise.resolve({ n: 1 }));
    renderHook(() => useLiveQuery("k1", fetcher, { topics: ["movie.*"], fallbackMs: 30_000 }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1); // the first load
    last().open();
    last().send("movie.refreshed");
    last().send("movie.renamed");
    last().send("series.imported"); // not ours
    await vi.advanceTimersByTimeAsync(499);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("keeps only a slow fallback poll", async () => {
    const fetcher = vi.fn(() => Promise.resolve(1));
    renderHook(() => useLiveQuery("k2", fetcher, { topics: ["x"], fallbackMs: 30_000 }));
    await flush();
    await vi.advanceTimersByTimeAsync(29_000);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("refetches when the socket comes back", async () => {
    const fetcher = vi.fn(() => Promise.resolve(1));
    renderHook(() => useLiveQuery("k3", fetcher, { topics: ["x"], fallbackMs: 30_000 }));
    await flush();
    last().open(); // the first connection isn't a reconnect
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(5000);
    last().drop();
    await vi.advanceTimersByTimeAsync(BACKOFF_MIN_MS);
    last().open();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("makes no requests while the tab is hidden, and catches up when shown", async () => {
    const fetcher = vi.fn(() => Promise.resolve(1));
    renderHook(() => useLiveQuery("k4", fetcher, { topics: ["x"], fallbackMs: 10_000 }));
    await flush();
    last().open();
    setVisibility("hidden");
    last().send("x");
    await vi.advanceTimersByTimeAsync(5 * 60_000);
    expect(fetcher).toHaveBeenCalledTimes(1);
    setVisibility("visible");
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("without a fallback poll, still catches up on an event missed while hidden", async () => {
    const fn = vi.fn();
    renderHook(() => useLiveRefresh(fn, { topics: ["x"], fallbackMs: null }));
    last().open();
    setVisibility("hidden");
    last().send("x");
    await vi.advanceTimersByTimeAsync(5000);
    expect(fn).not.toHaveBeenCalled();
    setVisibility("visible");
    expect(fn).toHaveBeenCalledTimes(1);
    setVisibility("hidden");
    setVisibility("visible"); // nothing missed this time
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("useLiveRefresh honours a filter", async () => {
    const fn = vi.fn();
    renderHook(() => useLiveRefresh(fn, { topics: ["integration.status"], filter: (ev) => (ev.data as { kind: string }).kind === "indexer", fallbackMs: null }));
    last().open();
    last().send("integration.status", { kind: "plex" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(fn).not.toHaveBeenCalled();
    last().send("integration.status", { kind: "indexer" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
