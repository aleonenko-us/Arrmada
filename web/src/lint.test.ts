import { describe, expect, it } from "vitest";
import { ESLint } from "eslint";

// The UI kit's bans (FE-14) are lint errors, so CI fails on a page that brings back a
// native dialog, a hand-built overlay or a page-local toast. This runs the repo's own
// eslint.config.js over snippets, so loosening a rule to "warn" fails here.

const eslint = new ESLint();

async function errors(code: string, filePath = "src/pages/Example.tsx"): Promise<string[]> {
  const [res] = await eslint.lintText(code, { filePath });
  return res.messages.filter((m) => m.severity === 2).map((m) => m.ruleId ?? "");
}

describe("the UI kit's lint bans", () => {
  it("rejects native confirm, alert and prompt", async () => {
    expect(await errors(`export const a = () => window.confirm("x");`)).toEqual(["no-restricted-properties"]);
    expect(await errors(`export const a = () => confirm("x");`)).toEqual(["no-restricted-globals"]);
    expect(await errors(`export const a = () => alert("x");`)).toEqual(["no-restricted-globals"]);
    expect(await errors(`export const a = () => window.prompt("x");`)).toEqual(["no-restricted-properties"]);
  }, 30_000);

  it("rejects a hand-built overlay outside src/ui, and allows it inside", async () => {
    // Spelled in two halves so this file doesn't trip the rule it tests.
    const cover = "fixed " + "inset-0";
    const overlay = `export const O = () => <div className="${cover} z-50" />;`;
    expect(await errors(overlay)).toEqual(["no-restricted-syntax"]);
    expect(await errors(overlay, "src/ui/Example.tsx")).toEqual([]);
  }, 30_000);

  it("rejects a page-local toast", async () => {
    expect(await errors(`import { useState } from "react";
export function P() { const [toast, setToast] = useState<string | null>(null); return toast ?? setToast; }`)).toEqual(["no-restricted-syntax"]);
    expect(await errors(`export function P() { const flash = (m: string) => m; return flash; }`)).toEqual(["no-restricted-syntax"]);
    // The kit's own hook under another name is fine.
    expect(await errors(`import { useToast } from "../ui";
export function P() { const toast = useToast(); return toast; }`)).toEqual([]);
  }, 30_000);
});

// FE-15: the jsx-a11y rules are errors, so a field, button or click target that a screen
// reader or a keyboard can't use fails CI rather than warning.
describe("accessibility lint", () => {
  it("rejects a label tied to nothing, an unnamed button and a click-only div", async () => {
    expect(await errors(`export const F = () => <><label>Name</label><input /></>;`)).toContain("jsx-a11y/label-has-associated-control");
    expect(await errors(`export const B = () => <button type="button" />;`)).toContain("jsx-a11y/control-has-associated-label");
    const clickDiv = await errors(`export const D = ({ go }: { go: () => void }) => <div onClick={go}>Open</div>;`);
    expect(clickDiv).toContain("jsx-a11y/click-events-have-key-events");
    expect(clickDiv).toContain("jsx-a11y/no-static-element-interactions");
  }, 30_000);

  it("accepts the labelled shapes the pages use", async () => {
    expect(await errors(`export const F = () => <label>Name <input /></label>;`)).toEqual([]);
    expect(await errors(`export const F = () => <><label htmlFor="n">Name</label><input id="n" /></>;`)).toEqual([]);
    // Text a couple of spans deep (a bold name over a faint hint) still names the label.
    expect(await errors(`export const F = ({ l }: { l: string }) => <label><input type="checkbox" /><span><b>{l}</b><span>hint</span></span></label>;`)).toEqual([]);
    expect(await errors(`export const B = () => <button type="button" aria-label="Close">✕</button>;`)).toEqual([]);
  }, 30_000);
});
