import { describe, expect, it } from "vitest";
import type { MediaRequest } from "./api";
import { requestStage } from "./requestStage";
import { STAGE, stageGroup, stageLabel } from "./stages";

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
