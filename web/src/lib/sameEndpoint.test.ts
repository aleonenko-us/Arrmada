import { describe, expect, it } from "vitest";
import { sameEndpoint, secretMoved } from "./sameEndpoint";

describe("sameEndpoint", () => {
  it("matches the server's normalising", () => {
    expect(sameEndpoint("http://192.168.1.10:9696", "http://192.168.1.10:9696/")).toBe(true);
    expect(sameEndpoint("HTTP://Prowlarr:9696", "http://prowlarr:9696")).toBe(true);
    expect(sameEndpoint("http://plex:80/", "http://plex")).toBe(true);
    expect(sameEndpoint("http://plex:32400?x=1", "http://plex:32400")).toBe(true);
    expect(sameEndpoint("http://plex:32400", "https://plex:32400")).toBe(false);
    expect(sameEndpoint("http://plex:32400", "http://evil.example:32400")).toBe(false);
    expect(sameEndpoint("http://host/sab", "http://host")).toBe(false);
  });
});

describe("secretMoved", () => {
  it("asks for the secret only when one is saved, none typed and the address changed", () => {
    const base = { saved: true, typed: "", url: "http://evil:1", savedUrl: "http://qb:8080" };
    expect(secretMoved(base)).toBe(true);
    expect(secretMoved({ ...base, typed: "pw" })).toBe(false);
    expect(secretMoved({ ...base, saved: false })).toBe(false);
    expect(secretMoved({ ...base, url: "http://QB:8080/" })).toBe(false);
  });
});
