import { describe, expect, it } from "vitest";
import { measurePathProblem, pauseFreeProblem, shortBytes, sizeAndFree } from "./diskGuard";

const GB = 1024 ** 3;
const TB = 1024 ** 4;

describe("sizeAndFree", () => {
  it("reads like a drive", () => {
    expect(sizeAndFree(TB, 312 * GB)).toBe("1.0 TB, 312 GB free");
    expect(sizeAndFree(4 * TB, 1.5 * TB)).toBe("4.0 TB, 1.5 TB free");
    expect(sizeAndFree(500 * GB, 9.5 * GB)).toBe("500 GB, 9.5 GB free");
  });
  it("says nothing without a size, and a full drive has 0 B free", () => {
    expect(sizeAndFree(0, 0)).toBeNull();
    expect(sizeAndFree(undefined, undefined)).toBeNull();
    expect(sizeAndFree(TB, 0)).toBe("1.0 TB, 0 B free");
  });
  it("rounds from 10 up", () => {
    expect(shortBytes(10.4 * GB)).toBe("10 GB");
    expect(shortBytes(9.94 * GB)).toBe("9.9 GB");
  });
});

describe("pauseFreeProblem", () => {
  it("takes whole GB, blank or 0 for off", () => {
    for (const ok of ["", " ", "0", "100", " 250 ", "1048576"]) expect(pauseFreeProblem(ok)).toBeNull();
  });
  it("refuses anything else", () => {
    for (const bad of ["1.5", "-1", "abc", "1048577"]) expect(pauseFreeProblem(bad)).toMatch(/whole number of GB/);
  });
});

describe("measurePathProblem", () => {
  it("takes a full path or nothing", () => {
    expect(measurePathProblem("")).toBeNull();
    expect(measurePathProblem("  ")).toBeNull();
    expect(measurePathProblem("/cache")).toBeNull();
    expect(measurePathProblem(" /mnt/pool ")).toBeNull();
  });
  it("refuses a relative path before sending it", () => {
    expect(measurePathProblem("cache")).toMatch(/full path starting with \//);
    expect(measurePathProblem("C:\\cache")).toMatch(/full path/);
  });
});
