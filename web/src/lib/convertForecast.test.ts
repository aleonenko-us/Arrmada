import { describe, expect, it } from "vitest";
import { aboutNights, aboutTime, backlogLine, fileForecast, nights, restartLoss } from "./convertForecast";

const DAY = 24 * 3600;

describe("convert forecast", () => {
  it("counts nights like the server", () => {
    expect(nights(DAY, "01:00", "07:00")).toBe(4);
    expect(nights(DAY + 1, "01:00", "07:00")).toBe(5);
    expect(nights(DAY, "22:00", "06:00")).toBe(3);
    expect(nights(DAY, "", "")).toBe(1);
    expect(nights(DAY, "03:00", "03:00")).toBe(1);
    expect(nights(0, "01:00", "07:00")).toBe(0);
  });

  it("words times and spans generously", () => {
    expect(aboutTime(DAY)).toBe("~24 h");
    expect(aboutTime(40 * 60)).toBe("~40 min");
    expect(aboutTime(60)).toBe("~5 min");
    expect(aboutTime(5 * DAY)).toBe("~5 days");
    expect(aboutNights(4)).toBe("~4 nights");
    expect(aboutNights(1)).toBe("~1 night");
    expect(aboutNights(21)).toBe("~3 weeks of nights");
    expect(aboutNights(270)).toBe("~9 months of nights");
  });

  it("writes Up next's and the Overview's lines", () => {
    expect(fileForecast(DAY, true, 4)).toBe("~24 h · ~4 nights");
    expect(fileForecast(DAY, false, 1)).toBe("~24 h");
    expect(fileForecast(0, true, 0)).toBe("");
    expect(backlogLine(270 * 6 * 3600, "01:00", "07:00")).toBe("Backlog ≈ 9 months of nights at 01:00–07:00");
    expect(backlogLine(3 * DAY, "", "")).toBe("Backlog ≈ 3 days at any time");
    expect(backlogLine(0, "01:00", "07:00")).toBe("");
    // The forecast changes with the hours.
    expect(backlogLine(40 * DAY, "01:00", "07:00")).not.toBe(backlogLine(40 * DAY, "22:00", "08:00"));
  });

  it("warns about a restart only past three hours of encoding", () => {
    expect(restartLoss(2 * 3600)).toBe("");
    expect(restartLoss(20 * 3600)).toBe("A restart or update now loses ~20 h of encoding");
  });
});
