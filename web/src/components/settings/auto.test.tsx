// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { ApiError } from "../../lib/api";
import { useAutoSave } from "../../lib/useAutoSave";
import { AutoNumber, AutoText, AutoToggle, wholeNumber } from "./auto";

// CFG-14: each control saves itself. Text saves on blur or Enter (never per keystroke),
// Esc reverts, a local check stops a bad value before it's sent, a server refusal shows
// under the field, a failed network save offers Retry, and a toggle flipped twice ends on
// the second value.

afterEach(cleanup);

function deferred<T = void>() {
  let resolve!: (v: T) => void, reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

// A server that stores whatever save() is given, so `saved` follows a successful save.
function TextHarness({ save, initial = "a", validate }: { save: (v: string) => Promise<void>; initial?: string; validate?: (v: string) => string | null }) {
  const [stored, setStored] = useState(initial);
  const field = useAutoSave(stored, async (v) => { await save(v); setStored(v); }, { validate });
  return <><AutoText label="Name" save={field} /><output data-testid="stored">{stored}</output></>;
}

const box = () => screen.getByRole("textbox");

describe("AutoText", () => {
  it("saves once on blur, not on every keystroke", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    render(<TextHarness save={save} />);
    fireEvent.change(box(), { target: { value: "ab" } });
    fireEvent.change(box(), { target: { value: "abc" } });
    expect(save).not.toHaveBeenCalled();
    await act(async () => { fireEvent.blur(box()); });
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith("abc");
    expect(screen.getByText("Saved ✓")).toBeTruthy();
    expect(screen.getByTestId("stored").textContent).toBe("abc");
  });

  it("saves on Enter, and the blur after it doesn't send it again", async () => {
    const d = deferred();
    const save = vi.fn().mockReturnValue(d.promise);
    render(<TextHarness save={save} />);
    fireEvent.change(box(), { target: { value: "b" } });
    await act(async () => {
      fireEvent.keyDown(box(), { key: "Enter" });
      fireEvent.blur(box());
    });
    expect(save).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Saving…")).toBeTruthy();
    await act(async () => { d.resolve(); });
    expect(screen.getByText("Saved ✓")).toBeTruthy();
  });

  it("does nothing when the value didn't change", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    render(<TextHarness save={save} />);
    await act(async () => { fireEvent.blur(box()); });
    expect(save).not.toHaveBeenCalled();
  });

  it("puts the saved value back on Esc", () => {
    const save = vi.fn().mockResolvedValue(undefined);
    render(<TextHarness save={save} />);
    fireEvent.change(box(), { target: { value: "half-typ" } });
    fireEvent.keyDown(box(), { key: "Escape" });
    expect((box() as HTMLInputElement).value).toBe("a");
    expect(save).not.toHaveBeenCalled();
  });

  it("shows a server refusal under the field, keeps the typed value, and offers no Retry", async () => {
    const save = vi.fn().mockRejectedValue(new ApiError("region must be a two-letter country code, e.g. AU", 400));
    render(<TextHarness save={save} />);
    fireEvent.change(box(), { target: { value: "Australia" } });
    await act(async () => { fireEvent.blur(box()); });
    expect(screen.getByRole("alert").textContent).toContain("two-letter country code");
    expect((box() as HTMLInputElement).value).toBe("Australia");
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.getByTestId("stored").textContent).toBe("a");
  });

  it("offers Retry after a network failure, and Retry saves it", async () => {
    const save = vi.fn().mockRejectedValueOnce(new TypeError("Failed to fetch")).mockResolvedValueOnce(undefined);
    render(<TextHarness save={save} />);
    fireEvent.change(box(), { target: { value: "b" } });
    await act(async () => { fireEvent.blur(box()); });
    expect(screen.getByRole("alert").textContent).toContain("Failed to fetch");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry" })); });
    expect(save).toHaveBeenLastCalledWith("b");
    expect(screen.getByTestId("stored").textContent).toBe("b");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("checks the value before sending it", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    function NumberHarness() {
      const [stored, setStored] = useState("10");
      const n = useAutoSave(stored, async (v) => { await save(v); setStored(v); }, { validate: wholeNumber(0, 1000, "Upgrades") });
      return <AutoNumber label="Upgrades" save={n} min={0} max={1000} />;
    }
    render(<NumberHarness />);
    fireEvent.change(box(), { target: { value: "" } });
    await act(async () => { fireEvent.blur(box()); });
    expect(save).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toContain("needs a number from 0 to 1000");
    // Letters never get in; a fixed value saves.
    fireEvent.change(box(), { target: { value: "2x5" } });
    expect((box() as HTMLInputElement).value).toBe("25");
    await act(async () => { fireEvent.blur(box()); });
    expect(save).toHaveBeenCalledWith("25");
  });
});

describe("AutoToggle", () => {
  function ToggleHarness({ save }: { save: (v: boolean) => Promise<void> }) {
    const [stored, setStored] = useState(false);
    const t = useAutoSave(stored, async (v) => { await save(v); setStored(v); });
    return <><AutoToggle label="Books" hint="" save={t} /><output data-testid="stored">{String(stored)}</output></>;
  }

  it("saves on change", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    render(<ToggleHarness save={save} />);
    await act(async () => { fireEvent.click(screen.getByRole("switch")); });
    expect(save).toHaveBeenCalledWith(true);
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true");
  });

  it("flips back when the save fails, and says why", async () => {
    const save = vi.fn().mockRejectedValue(new ApiError("Only an admin can change the Books module", 403));
    render(<ToggleHarness save={save} />);
    await act(async () => { fireEvent.click(screen.getByRole("switch")); });
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("alert").textContent).toContain("Only an admin");
  });

  it("flipped twice quickly, ends on the second value — sent after the first", async () => {
    const first = deferred();
    const calls: boolean[] = [];
    const save = vi.fn((v: boolean) => { calls.push(v); return calls.length === 1 ? first.promise : Promise.resolve(); });
    render(<ToggleHarness save={save} />);
    await act(async () => { fireEvent.click(screen.getByRole("switch")); }); // on (in flight)
    await act(async () => { fireEvent.click(screen.getByRole("switch")); }); // back off: must still be sent
    expect(calls).toEqual([true]); // the second waits for the first
    await act(async () => { first.resolve(); });
    expect(calls).toEqual([true, false]);
    expect(screen.getByTestId("stored").textContent).toBe("false");
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("false");
  });
});
