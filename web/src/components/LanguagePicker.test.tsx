// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { LanguagePicker } from "./LanguagePicker";
import { langName, searchLanguages, type Language } from "../lib/languages";

// SUB-31: one searchable, ordered language picker over the server's table.

const table: Language[] = [
  { code: "en", name: "English", regional: false },
  { code: "fr", name: "French", regional: false },
  { code: "nb", name: "Norwegian Bokmål", regional: false },
  { code: "pt", name: "Portuguese", regional: false },
  { code: "es", name: "Spanish", regional: false },
  { code: "pt-br", name: "Portuguese (Brazil)", regional: true },
  { code: "pt-pt", name: "Portuguese (Portugal)", regional: true },
  { code: "es-419", name: "Spanish (Latin America)", regional: true },
];

afterEach(cleanup);

describe("searchLanguages", () => {
  it("offers the base language before its regions", () => {
    expect(searchLanguages(table, "port").map((l) => l.code)).toEqual(["pt", "pt-br", "pt-pt"]);
  });
  it("finds by code, by word and without accents", () => {
    expect(searchLanguages(table, "fr")[0].code).toBe("fr");
    expect(searchLanguages(table, "brazil").map((l) => l.code)).toEqual(["pt-br"]);
    expect(searchLanguages(table, "bokmal").map((l) => l.code)).toEqual(["nb"]);
    expect(searchLanguages(table, "latin").map((l) => l.code)).toEqual(["es-419"]);
  });
  it("leaves out what's already chosen, and nothing for an empty query", () => {
    expect(searchLanguages(table, "port", ["pt"]).map((l) => l.code)).toEqual(["pt-br", "pt-pt"]);
    expect(searchLanguages(table, "  ")).toEqual([]);
  });
});

describe("langName", () => {
  it("names from the table, falls back to the code", () => {
    expect(langName("pt-br", table)).toBe("Portuguese (Brazil)");
    expect(langName("xx", table)).toBe("XX");
    expect(langName("en", null)).toBe("EN");
  });
});

describe("LanguagePicker", () => {
  it("searches and adds a regional variant at the end", () => {
    const change = vi.fn();
    render(<LanguagePicker value={["en"]} onChange={change} languages={table} />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "port" } });
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["Portuguesept", "Portuguese (Brazil)pt-br", "Portuguese (Portugal)pt-pt"]);
    fireEvent.mouseDown(screen.getByRole("option", { name: /Portuguese \(Brazil\)/ }));
    expect(change).toHaveBeenCalledWith(["en", "pt-br"]);
  });

  it("adds the highlighted match with the keyboard", () => {
    const change = vi.fn();
    render(<LanguagePicker value={[]} onChange={change} languages={table} />);
    const box = screen.getByRole("combobox");
    fireEvent.change(box, { target: { value: "port" } });
    fireEvent.keyDown(box, { key: "ArrowDown" });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(change).toHaveBeenCalledWith(["pt-br"]);
  });

  it("reorders and removes, keeping the order it was given", () => {
    const change = vi.fn();
    render(<LanguagePicker value={["en", "fr", "pt-br"]} onChange={change} languages={table} firstNote="untagged counts as this" />);
    expect(screen.getByText("untagged counts as this")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Move Portuguese (Brazil) up" }));
    expect(change).toHaveBeenLastCalledWith(["en", "pt-br", "fr"]);
    fireEvent.click(screen.getByRole("button", { name: "Move English down" }));
    expect(change).toHaveBeenLastCalledWith(["fr", "en", "pt-br"]);
    fireEvent.click(screen.getByRole("button", { name: "Remove French" }));
    expect(change).toHaveBeenLastCalledWith(["en", "pt-br"]);
    expect((screen.getByRole("button", { name: "Move English up" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Move Portuguese (Brazil) down" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
