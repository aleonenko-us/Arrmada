import type { HTMLAttributes } from "react";

export type PageWidth = "default" | "wide";

export interface PageProps extends HTMLAttributes<HTMLDivElement> {
  /** wide is for the poster grids (Discover, the libraries, an author's shelf, Activity). */
  width?: PageWidth;
  /** Vertical padding and layout; replaces the default "py-6". */
  className?: string;
}

// Page is the content column under the PageHeader. There are two widths, so the content's
// left edge sits in the same place on every page of a kind: default (1240 px) for most
// pages and wide (1600 px) for the poster grids. The side gutter is 16 px on a phone and
// 24 px from sm up. Form-like content inside a page sits in a left-aligned max-w-form
// (760 px) column rather than a narrower, centred page.
export function Page({ width = "default", className = "py-6", children, ...rest }: PageProps) {
  return (
    <div className={`mx-auto w-full ${width === "wide" ? "max-w-page-wide" : "max-w-page"} px-4 sm:px-6 ${className}`} {...rest}>
      {children}
    </div>
  );
}
