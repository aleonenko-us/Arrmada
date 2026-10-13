// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { FIRST, useProgressive } from "./progressive";

// A big library renders a few screens of cards and grows as it scrolls (it used to be
// every card at once: most of a second of main-thread work for 2,500 movies).

let observers: { cb: IntersectionObserverCallback; el?: Element }[] = [];
class FakeObserver {
  cb: IntersectionObserverCallback;
  constructor(cb: IntersectionObserverCallback) { this.cb = cb; observers.push(this); }
  observe(el: Element) { (this as { el?: Element }).el = el; }
  disconnect() { observers = observers.filter((o) => o !== this); }
  unobserve() {}
  takeRecords() { return []; }
}
// near says the end-of-list marker came within reach of the screen.
function near() {
  const o = observers[observers.length - 1];
  act(() => o.cb([{ isIntersecting: true } as IntersectionObserverEntry], o as unknown as IntersectionObserver));
}

function List({ n, reset = "" }: { n: number; reset?: string }) {
  const items = Array.from({ length: n }, (_, i) => i);
  const { shown, sentinel } = useProgressive(items, "test", reset);
  return <><ul>{shown.map((i) => <li key={i}>{i}</li>)}</ul>{sentinel}</>;
}
const rendered = () => screen.queryAllByRole("listitem").length;

beforeEach(() => { observers = []; sessionStorage.clear(); vi.stubGlobal("IntersectionObserver", FakeObserver); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("useProgressive", () => {
  it("renders a short list whole, with nothing to watch", () => {
    render(<MemoryRouter><List n={10} /></MemoryRouter>);
    expect(rendered()).toBe(10);
    expect(observers).toHaveLength(0);
  });

  it("renders the first screens, then more as the end comes near, up to the whole list", () => {
    render(<MemoryRouter><List n={200} /></MemoryRouter>);
    expect(rendered()).toBe(FIRST);
    near();
    expect(rendered()).toBe(FIRST * 2);
    near();
    expect(rendered()).toBe(200);
    expect(observers).toHaveLength(0); // the whole list: the marker is gone
  });

  it("starts again from the top when the filter or search changes", () => {
    const { rerender } = render(<MemoryRouter><List n={300} reset="all" /></MemoryRouter>);
    near();
    expect(rendered()).toBe(FIRST * 2);
    rerender(<MemoryRouter><List n={300} reset="wanted" /></MemoryRouter>);
    expect(rendered()).toBe(FIRST);
  });

  it("keeps how far it grew for the history entry, so Back renders that much at once", () => {
    const first = render(<MemoryRouter initialEntries={[{ pathname: "/movies", key: "k1" }]}><List n={300} /></MemoryRouter>);
    near();
    near();
    expect(rendered()).toBe(FIRST * 3);
    first.unmount();
    render(<MemoryRouter initialEntries={[{ pathname: "/movies", key: "k1" }]}><List n={300} /></MemoryRouter>);
    expect(rendered()).toBe(FIRST * 3);
  });

  it("renders everything where there's no IntersectionObserver", () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    render(<MemoryRouter><List n={200} /></MemoryRouter>);
    expect(rendered()).toBe(200);
  });
});
