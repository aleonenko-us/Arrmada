// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { homeFor, keepNextAcrossRedirect, nextAfterSignIn } from "./session";

// CFG-21: staff land on the Dashboard after signing in, everyone else on Discover — unless
// the sign-in was for a particular page.

afterEach(() => {
  sessionStorage.clear();
  window.history.replaceState(null, "", "/");
});

describe("where a sign-in lands", () => {
  it("sends staff to the Dashboard and everyone else to Discover", () => {
    expect(homeFor("admin")).toBe("/");
    expect(homeFor("manager")).toBe("/");
    expect(homeFor("requester")).toBe("/discover");
    expect(homeFor("readonly")).toBe("/discover");
    expect(homeFor(undefined)).toBe("/discover");
    expect(nextAfterSignIn("admin")).toBe("/");
    expect(nextAfterSignIn("requester")).toBe("/discover");
  });

  it("still returns to the page that was asked for", () => {
    window.history.replaceState(null, "", "/?next=%2Fmovies%2F12");
    expect(nextAfterSignIn("admin")).toBe("/movies/12");
    window.history.replaceState(null, "", "/series/4?tab=episodes");
    expect(nextAfterSignIn("requester")).toBe("/series/4?tab=episodes");
  });

  it("doesn't pin a Plex redirect sign-in to Discover before it knows who signed in", () => {
    keepNextAcrossRedirect();
    // plex.tv sends the page back to "/?plexpin=…"; an admin then lands on the Dashboard.
    window.history.replaceState(null, "", "/?plexpin=7");
    expect(nextAfterSignIn("admin")).toBe("/");
  });

  it("keeps a page asked for across the Plex redirect", () => {
    window.history.replaceState(null, "", "/requests?id=3");
    keepNextAcrossRedirect();
    window.history.replaceState(null, "", "/?plexpin=7");
    expect(nextAfterSignIn("admin")).toBe("/requests?id=3");
  });
});
