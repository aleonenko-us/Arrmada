import { describe, expect, it } from "vitest";
import { isActivityTab, legacyActivityPath } from "./activity";

describe("legacyActivityPath", () => {
  it("opens the matching tab for each page Activity replaced", () => {
    expect(legacyActivityPath("downloads", "")).toBe("/activity/queue");
    expect(legacyActivityPath("review", "")).toBe("/activity/needs-you");
    expect(legacyActivityPath("history", "")).toBe("/activity/history");
    expect(legacyActivityPath("blocklist", "")).toBe("/activity/blocklist");
    expect(legacyActivityPath("activity", "")).toBe("/activity/queue");
  });

  it("translates Downloads' old tabs into Activity's tabs and sub-views", () => {
    expect(legacyActivityPath("downloads", "?tab=downloads")).toBe("/activity/queue");
    expect(legacyActivityPath("downloads", "?tab=seeding")).toBe("/activity/queue?show=seeding");
    expect(legacyActivityPath("downloads", "?tab=searching")).toBe("/activity/wanted");
    expect(legacyActivityPath("downloads", "?tab=upcoming")).toBe("/activity/wanted?show=upcoming");
  });

  it("keeps the problems filter (the old Needs-you link) and every other parameter", () => {
    expect(legacyActivityPath("downloads", "?show=problems")).toBe("/activity/queue?filter=problems");
    expect(legacyActivityPath("downloads", "?tab=seeding&show=problems")).toBe("/activity/queue?show=seeding&filter=problems");
    expect(legacyActivityPath("blocklist", "?q=x", "#top")).toBe("/activity/blocklist?q=x#top");
    expect(legacyActivityPath("activity", "?show=seeding")).toBe("/activity/queue?show=seeding");
  });
});

describe("isActivityTab", () => {
  it("accepts the five tabs only", () => {
    for (const t of ["queue", "needs-you", "wanted", "history", "blocklist"]) expect(isActivityTab(t)).toBe(true);
    for (const t of [undefined, "", "downloads", "review", "toString"]) expect(isActivityTab(t)).toBe(false);
  });
});
