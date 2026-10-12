import { describe, expect, it } from "vitest";
import { followLine } from "./authorFollow";

describe("followLine", () => {
  const now = Date.parse("2026-10-13T12:00:00Z");
  it("says when the check last looked, reading the server's UTC stamp", () => {
    expect(followLine({ works_checked_at: "2026-10-10 12:00:00", new_books_found: 0 }, now)).toBe("Checks weekly for new books — last checked 3d ago");
  });
  it("counts the books it has added", () => {
    expect(followLine({ works_checked_at: "2026-10-13 09:00:00", new_books_found: 1 }, now)).toBe("Checks weekly for new books — last checked 3h ago · 1 new book added so far");
    expect(followLine({ works_checked_at: "2026-10-13 09:00:00", new_books_found: 4 }, now)).toBe("Checks weekly for new books — last checked 3h ago · 4 new books added so far");
  });
  it("has not looked yet", () => {
    expect(followLine({}, now)).toBe("Checks weekly for new books — first check soon");
  });
});
