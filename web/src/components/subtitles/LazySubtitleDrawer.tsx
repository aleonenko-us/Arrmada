import { lazy, Suspense } from "react";
import type { SubtitleDrawerProps } from "../SubtitleDrawer";

// The drawer is staff-only and opened by a button, so its code (and its API calls) load
// on the first open, not with the page that offers it.
const Drawer = lazy(() => import("../SubtitleDrawer"));

export function LazySubtitleDrawer(props: SubtitleDrawerProps) {
  return (
    <Suspense fallback={null}>
      <Drawer {...props} />
    </Suspense>
  );
}
