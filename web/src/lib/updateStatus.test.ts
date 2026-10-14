import { describe, expect, it } from "vitest";
import type { UpdateStatus } from "./api";
import { INSTALL_LINE, noteBlocks, updateHeadline, updateHint, versionBadge } from "./updateStatus";

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

describe("a build on an install that follows the releases", () => {
  const build = { ...base, channel: "build" as const, current: "2026.10.14", available: false };

  it("is offered the release that includes it, or told it's newer", () => {
    expect(updateHeadline({ ...build, available: true, ahead: 0 })).toEqual({ text: "Update available: v1.3.0", tone: "update" });
    expect(updateHeadline({ ...build, ahead: 5 })).toEqual({ text: "This build is newer than the latest release (v1.3.0).", tone: "ok" });
    expect(updateHeadline({ ...build, ahead: 0 }).text).toBe("Up to date: this build is v1.3.0.");
    expect(updateHeadline({ ...build, ahead: null }).tone).toBe("muted");
    expect(updateHeadline({ ...build, latest: null }).text).toBe("No release has been published yet.");
  });
});

describe("updateHint", () => {
  it("names the way to update for each channel", () => {
    expect(updateHint(base)).toEqual({ text: expect.stringMatching(/install line.*docker compose pull/), command: INSTALL_LINE });
    expect(updateHint({ ...base, available: false })).toBeNull();
    expect(updateHint({ ...base, channel: "build", available: true, ahead: 0 })?.text).toMatch(/move it to that release/);
    expect(updateHint({ ...base, channel: "build", available: false, ahead: 3 })?.command).toBe("ARRMADA_CHANNEL=main");
    expect(updateHint({ ...base, channel: "main", available: false, behind: 4 })).toEqual({ text: expect.stringMatching(/pulls the newest code/), command: INSTALL_LINE });
    expect(updateHint({ ...base, channel: "main", available: false, behind: 0 })).toBeNull();
    expect(updateHint({ ...base, enabled: false })).toBeNull();
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

describe("versionBadge", () => {
  const release: UpdateStatus = { ...base, current: "v0.2.0", available: false, latest: { ...base.latest!, tag: "v0.2.0" } };
  const newer = { ...base.latest!, tag: "v0.2.1" };

  it("names a release build by its tag, and says when it's current", () => {
    expect(versionBadge("v0.2.0", "c9d20ec", release)).toEqual({ label: "v0.2.0", update: false, title: "Arrmada v0.2.0 — up to date" });
  });

  it("puts a dot on a release with a newer one out, naming it", () => {
    const u = { ...release, available: true, latest: newer };
    expect(versionBadge("v0.2.0", "c9d20ec", u)).toEqual({ label: "v0.2.0", update: true, title: "Arrmada v0.2.0 — update available (v0.2.1)" });
  });

  it("names a build of main by its short commit, with how far behind it is", () => {
    const main: UpdateStatus = { ...base, channel: "main", current: "2026.10.15", available: false };
    expect(versionBadge("2026.10.15", "c9d20ec4abcdef", { ...main, behind: 0 })).toEqual({ label: "main · c9d20ec", update: false, title: "Arrmada main · c9d20ec — up to date" });
    expect(versionBadge("2026.10.15", "c9d20ec", { ...main, behind: 3 })).toEqual({ label: "main · c9d20ec", update: true, title: "Arrmada main · c9d20ec — update available (3 commits behind main)" });
    expect(versionBadge("2026.10.15", "c9d20ec", { ...main, behind: 1 }).title).toBe("Arrmada main · c9d20ec — update available (1 commit behind main)");
  });

  it("a commit build on a stable install is main's code too; a release that includes it is the update", () => {
    const build: UpdateStatus = { ...base, channel: "build", current: "2026.10.15", available: true, latest: newer };
    expect(versionBadge("2026.10.15", "c9d20ec", build)).toEqual({ label: "main · c9d20ec", update: true, title: "Arrmada main · c9d20ec — update available (v0.2.1)" });
    expect(versionBadge("2026.10.15", "c9d20ec", { ...build, available: false, ahead: 2 }).update).toBe(false);
  });

  it("keeps the -dirty mark of a build with local edits", () => {
    expect(versionBadge("2026.10.15-dirty", "c9d20ec").label).toBe("main · c9d20ec-dirty");
    expect(versionBadge("v0.2.0-dirty", "c9d20ec").label).toBe("v0.2.0-dirty");
  });

  it("says dev for a build that doesn't know its commit", () => {
    expect(versionBadge("0.0.0-dev", "unknown")).toEqual({ label: "dev", update: false, title: "Arrmada dev" });
    expect(versionBadge("docker", "", null).label).toBe("dev");
    expect(versionBadge("2026.10.15", "unknown", { ...base, channel: "dev", available: false, behind: null }).label).toBe("dev");
  });

  it("reads the build itself when there's no check to ask (a manager), with no dot", () => {
    expect(versionBadge("v0.2.0", "c9d20ec")).toEqual({ label: "v0.2.0", update: false, title: "Arrmada v0.2.0" });
    expect(versionBadge("2026.10.15", "c9d20ec", null).label).toBe("main · c9d20ec");
  });

  it("takes the check's channel over the shape of the version (a tagged commit on a main install)", () => {
    expect(versionBadge("v0.2.0", "c9d20ec", { ...base, channel: "main", current: "v0.2.0", available: false, behind: 0 }).label).toBe("main · c9d20ec");
  });

  it("shows no dot while the check is off, hasn't run or couldn't compare", () => {
    const u = { ...release, available: true, latest: newer };
    expect(versionBadge("v0.2.0", "c9d20ec", { ...u, enabled: false })).toEqual({ label: "v0.2.0", update: false, title: "Arrmada v0.2.0" });
    expect(versionBadge("v0.2.0", "c9d20ec", { ...u, checked_at: null }).update).toBe(false);
    const main: UpdateStatus = { ...base, channel: "main", current: "2026.10.15", available: false, behind: null };
    expect(versionBadge("2026.10.15", "c9d20ec", main)).toEqual({ label: "main · c9d20ec", update: false, title: "Arrmada main · c9d20ec" });
  });
});
