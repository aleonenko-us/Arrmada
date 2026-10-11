import { describe, expect, it } from "vitest";
import { patchProgress, progressByHash, queueChangedFor } from "./queueLive";

const progress = { topic: "queue.progress", data: [{ hash: "AAAA", progress: 0.5, eta_seconds: 60, state: "downloading" }] };

describe("queue live helpers", () => {
  it("reads queue.progress by lower-cased hash, and ignores other topics", () => {
    expect(progressByHash(progress)?.get("aaaa")?.progress).toBe(0.5);
    expect(progressByHash({ topic: "queue.changed", data: [] })).toBeNull();
  });

  it("says whether queue.changed names one of a page's torrents", () => {
    const ev = { topic: "queue.changed", data: { added: [], removed: ["aaaa"], changed: [] } };
    expect(queueChangedFor(ev, ["AAAA"])).toBe(true);
    expect(queueChangedFor(ev, ["bbbb"])).toBe(false);
    expect(queueChangedFor(progress, ["aaaa"])).toBe(false);
  });

  it("moves only the bars it covers, and keeps the array when nothing moved", () => {
    const rows = [{ id: 1, d: { hash: "aaaa", progress: 0.2 } }, { id: 2, d: { hash: "cccc", progress: 0.9 } }, { id: 3, d: null }];
    const live = progressByHash(progress)!;
    const next = patchProgress(rows, live, (r) => r.d, (r, p) => ({ ...r, d: { ...r.d!, progress: p.progress } }));
    expect(next[0].d?.progress).toBe(0.5);
    expect(next[1]).toBe(rows[1]);
    expect(patchProgress(next, live, (r) => r.d, (r) => r)).toBe(next);
  });
});
