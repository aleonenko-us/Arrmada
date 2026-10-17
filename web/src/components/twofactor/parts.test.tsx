// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ToastProvider } from "../../ui";
import { groupSecret, QrCode, RecoveryCodes } from "./parts";

afterEach(cleanup);

describe("two-factor setup pieces", () => {
  it("groups the key in fours, the way it's typed into an app", () => {
    expect(groupSecret("JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP");
    expect(groupSecret("ABCDE")).toBe("ABCD E");
  });

  it("draws the QR code dark on white with a quiet zone, whatever the theme", () => {
    const { container } = render(<QrCode qr={{ size: 21, path: "M0 0h7v1h-7z" }} />);
    const svg = screen.getByRole("img", { name: /QR code/ });
    expect(svg.getAttribute("viewBox")).toBe("-4 -4 29 29");
    expect(container.querySelector("rect")?.getAttribute("fill")).toBe("#ffffff");
    expect(container.querySelector("path")?.getAttribute("d")).toBe("M0 0h7v1h-7z");
  });

  it("lists every recovery code once, with copy and download", () => {
    const codes = ["abcde-fghjk", "mnpqr-stuvw"];
    render(<ToastProvider><RecoveryCodes codes={codes} onDone={() => {}} /></ToastProvider>);
    for (const c of codes) expect(screen.getByText(c)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Download" })).toBeTruthy();
    expect(screen.getByText(/won’t be shown again/)).toBeTruthy();
  });
});
