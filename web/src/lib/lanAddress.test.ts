import { describe, expect, it } from "vitest";
import { lanOnly } from "./lanAddress";

describe("lanOnly", () => {
  it("flags addresses only reachable at home", () => {
    for (const a of [
      "http://192.168.1.10:8096", "10.0.0.5:8096", "http://172.20.3.4", "http://127.0.0.1:8096",
      "http://localhost:8096", "https://jellyfin.local", "http://nas.lan:8096", "http://box.home.arpa",
      "http://jellyfin:8096", "http://169.254.10.1", "http://[::1]:8096", "http://[fd12:3456::1]", "http://[fe80::1]",
    ]) expect(lanOnly(a), a).toBe(true);
  });

  it("leaves public addresses alone", () => {
    for (const a of [
      "https://jellyfin.example.com", "jf.example.org:8920", "http://8.8.8.8:8096", "http://172.32.0.1",
      "https://100.101.102.103", "http://[2001:db8::1]", "", "   ",
    ]) expect(lanOnly(a), a).toBe(false);
  });
});
