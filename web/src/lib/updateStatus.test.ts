import { describe, expect, it } from "vitest";
import type { UpdateStatus } from "./api";
import { noteBlocks, updateHeadline } from "./updateStatus";

const base: UpdateStatus = {
  enabled: true, current: "v1.2.0", channel: "release", checked_at: "2026-10-13T09:00:00Z",
  latest: { tag: "v1.3.0", name: "", url: "", notes: "", notes_truncated: false, published_at: "2026-10-12T09:00:00Z" },
  available: true, behind: null,
};

describe("updateHeadline", () => {
  it("names the newer release", () => {
    expect(updateHeadline(base)).toEqual({ text: "Update available: v1.3.0", tone: "update" });
  });

  it("says up to date, off, not yet and nothing published", () => {
    expect(updateHeadline({ ...base, available: false, current: "v1.3.0" }).text).toBe("Up to date: v1.3.0 is the newest release.");
    expect(updateHeadline({ ...base, enabled: false }).text).toBe("The update check is off.");
    expect(updateHeadline({ ...base, checked_at: null }).text).toBe("Not checked yet.");
    expect(updateHeadline({ ...base, checked_at: null, problem: "Couldn't reach GitHub" }).text).toBe("Couldn't check for updates yet.");
    expect(updateHeadline({ ...base, available: false, latest: null }).text).toBe("No release has been published yet.");
  });

  it("counts commits behind main for a build of main", () => {
    const main = { ...base, channel: "main" as const, current: "2026.10.08", available: false };
    expect(updateHeadline({ ...main, behind: 3 })).toEqual({ text: "3 commits behind main.", tone: "update" });
    expect(updateHeadline({ ...main, behind: 1 }).text).toBe("1 commit behind main.");
    expect(updateHeadline({ ...main, behind: 0 }).tone).toBe("ok");
    expect(updateHeadline({ ...main, behind: null }).tone).toBe("muted");
  });
});

describe("noteBlocks", () => {
  it("keeps headings and bullets as text, drops blank lines and markers", () => {
    expect(noteBlocks("## What's Changed\n\n* **Faster** imports by @a in https://github.com/x/pull/1\n- `fix` <b>this</b>\nThanks")).toEqual([
      { kind: "heading", text: "What's Changed" },
      { kind: "bullet", text: "Faster imports by @a in https://github.com/x/pull/1" },
      { kind: "bullet", text: "fix <b>this</b>" },
      { kind: "text", text: "Thanks" },
    ]);
  });
});
