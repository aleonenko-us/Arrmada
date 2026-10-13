// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { Field } from "./Field";
import { Input } from "./Input";
import { Select } from "./Select";
import { Switch } from "./Switch";

afterEach(cleanup);

describe("Field", () => {
  it("names its control, so getByLabelText finds it and a label click focuses it", () => {
    render(<Field label="Feed URL" hint="Your tracker's RSS link"><Input /></Field>);
    const box = screen.getByLabelText("Feed URL");
    expect(box.tagName).toBe("INPUT");
    expect(box.getAttribute("aria-describedby")).toBeTruthy();
    expect(document.getElementById(box.getAttribute("aria-describedby")!)?.textContent).toBe("Your tracker's RSS link");
    // The label's control is the box, which is what makes a click on it focus the box.
    expect((screen.getByText("Feed URL") as HTMLLabelElement).control).toBe(box);
  });

  it("keeps a control's own id and description", () => {
    render(<Field label="Size" error="Too big"><Select id="size" aria-describedby="other"><option>1</option></Select></Field>);
    const sel = screen.getByLabelText("Size");
    expect(sel.id).toBe("size");
    expect(sel.getAttribute("aria-describedby")).toMatch(/^other /);
    expect(sel.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole("alert").textContent).toBe("Too big");
  });

  it("labels a control by controlId when other things sit beside it", () => {
    render(<Field label="Language" controlId="lang"><span><select id="lang"><option>en</option></select> Saved</span></Field>);
    expect(screen.getByLabelText("Language").id).toBe("lang");
  });
});

describe("Switch", () => {
  function Harness({ text }: { text?: boolean }) {
    const [on, setOn] = useState(false);
    return text ? <Switch checked={on} onChange={setOn}>Monitor</Switch> : <Switch checked={on} onChange={setOn} label="Auto-ensure movies" />;
  }

  it("is a switch named by its label that flips aria-checked on click", () => {
    render(<Harness />);
    const sw = screen.getByRole("switch", { name: "Auto-ensure movies" });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(sw);
    expect(sw.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(sw);
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("is a native button, so Space and Enter flip it with no key handler of its own", () => {
    render(<Harness />);
    const sw = screen.getByRole("switch");
    expect(sw.tagName).toBe("BUTTON");
    expect(sw.getAttribute("type")).toBe("button");
  });

  it("takes its name from visible text inside it", () => {
    render(<Harness text />);
    const sw = screen.getByRole("switch", { name: "Monitor" });
    expect(sw.getAttribute("aria-label")).toBeNull();
  });

  it("is flipped by a <label htmlFor> beside it", () => {
    function Labelled() {
      const [on, setOn] = useState(false);
      return <><label htmlFor="sw">Tidy tracks</label><Switch id="sw" checked={on} onChange={setOn} /></>;
    }
    render(<Labelled />);
    fireEvent.click(screen.getByText("Tidy tracks"));
    expect(screen.getByRole("switch", { name: "Tidy tracks" }).getAttribute("aria-checked")).toBe("true");
  });
});
