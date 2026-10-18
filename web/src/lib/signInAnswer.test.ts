import { describe, expect, it } from "vitest";
import type { AuthUser } from "./api";
import { answered } from "./signInAnswer";

describe("answered", () => {
  const user = { id: 3, username: "deckhand", role: "requester" } as AuthUser;

  it("is null while the poll is pending", () => {
    expect(answered({ pending: true })).toBeNull();
    expect(answered({})).toBeNull();
  });

  it("is the user once signed in", () => {
    expect(answered({ user })).toEqual({ user });
  });

  it("is the two-factor ticket for a staff account that has it on", () => {
    expect(answered({ two_factor: "code", ticket: "t-1" })).toEqual({ two_factor: "code", ticket: "t-1" });
    expect(answered({ two_factor: "setup", ticket: "t-2" })).toEqual({ two_factor: "setup", ticket: "t-2" });
  });
});
