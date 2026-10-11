import { useEffect } from "react";
import { useMatches } from "react-router-dom";

// The browser tab title has two sources. Each route carries a fixed title in its handle
// ("Movies"), which the layout applies as soon as the route matches, before the page's
// code has even loaded. A page that knows something better ("Dune") sets it with useTitle,
// and that wins until the page goes away. Keeping both here, rather than letting the last
// effect write document.title, means the order React runs the effects in doesn't matter.

export interface RouteHandle {
  title?: string;
}

let routeTitle = "";
let pageTitle = "";
let count = 0;

// count is what waits on the signed-in staff member (requests to approve, imports to
// review), shown as "(3) " in front so a background tab says so.
export function formatTitle(page: string, route: string, count = 0): string {
  const t = page || route;
  const base = t ? `${t} · Arrmada` : "Arrmada";
  return count > 0 ? `(${count > 99 ? "99+" : count}) ${base}` : base;
}

function apply() {
  document.title = formatTitle(pageTitle, routeTitle, count);
}

/** The staff shell's Needs-you count for the tab title (0 hides it). */
export function setTitleCount(n: number): void {
  if (n === count) return;
  count = n;
  apply();
}

// useDocumentTitle is called once per layout: it reads the deepest matched route's handle.
export function useDocumentTitle(): void {
  const matches = useMatches();
  let title = "";
  for (const m of matches) {
    const h = m.handle as RouteHandle | undefined;
    if (h?.title) title = h.title;
  }
  useEffect(() => {
    routeTitle = title;
    apply();
  }, [title]);
}

// useTitle lets a page name the tab after what it shows (a movie, an author). An empty
// title leaves the route's own title in place.
export function useTitle(title: string | undefined): void {
  useEffect(() => {
    if (!title) return;
    pageTitle = title;
    apply();
    return () => {
      if (pageTitle === title) pageTitle = "";
      apply();
    };
  }, [title]);
}
