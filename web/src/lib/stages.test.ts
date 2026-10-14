import { describe, expect, it } from "vitest";
import type { MediaRequest } from "./api";
import { requestStage } from "./requestStage";
import { STAGE, stageGroup, stageLabel, usenetStage } from "./stages";

describe("stageGroup", () => {
  it("splits the queue into Downloading, Finishing and Seeding by stage", () => {
    for (const s of ["queued", "fetching_metadata", "downloading", "stalled", "paused", "paused_by_guard", "failed"]) expect(stageGroup(s, 1)).toBe("downloading");
    for (const s of ["downloaded", "importing", "import_retrying", "needs_review", "wrong_category"]) expect(stageGroup(s, 1)).toBe("finishing");
    for (const s of ["imported", "seeding", "not_managed"]) expect(stageGroup(s, 1)).toBe("seeding");
  });
  it("falls back to progress without a stage", () => {
    expect(stageGroup(undefined, 0.4)).toBe("downloading");
    expect(stageGroup(undefined, 1)).toBe("seeding");
  });
  // Nothing on Usenet seeds: a usenet job is unpacked, imported, then gone.
  it("keeps a usenet job out of Seeding: Unpacking and Imported sit under Finishing", () => {
    expect(stageGroup("unpacking", 1)).toBe("finishing");
    for (const s of ["imported", "seeding", "not_managed", undefined]) expect(stageGroup(s, 1, true)).toBe("finishing");
    expect(stageGroup("failed", 0.3, true)).toBe("downloading");
    expect(stageGroup(undefined, 0.3, true)).toBe("downloading");
  });
  it("reads a finished usenet job as Imported, never Seeding", () => {
    expect(usenetStage("seeding")).toBe("imported");
    expect(usenetStage("unpacking")).toBe("unpacking");
    expect(stageLabel(usenetStage("seeding"))).toBe("Imported");
    expect(stageLabel("unpacking")).toBe("Unpacking");
  });
});

describe("stageLabel", () => {
  it("names the Finishing stages", () => {
    expect(["importing", "needs_review", "import_retrying", "wrong_category"].map(stageLabel)).toEqual(["Importing", "Held", "Import failed", "Wrong category"]);
    expect(stageLabel("toString")).toBeUndefined();
  });
});

// The requester's card and the staff queue call the same download by the same word.
describe("one vocabulary", () => {
  const rq = (stage: string, note?: string) => ({ media_type: "movie", status: "approved", tracking: { stage, note } }) as unknown as MediaRequest;
  it("uses the shared words on request cards", () => {
    for (const s of ["queued", "downloading", "paused", "failed", "importing"] as const) {
      expect(requestStage(rq(s)).badge).toBe(STAGE[s].label);
    }
  });
  it("shows a held or retrying import as Importing, being checked", () => {
    expect(requestStage(rq("importing", "Being checked"))).toMatchObject({ badge: "Importing", detail: "Being checked" });
    expect(requestStage(rq("importing")).detail).toBe("Adding to the library…");
  });
});
