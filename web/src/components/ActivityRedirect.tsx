import { Navigate, useLocation } from "react-router-dom";
import { legacyActivityPath, type LegacyActivityPage } from "../lib/activity";

// ActivityRedirect sends a page the Activity page replaced (and the bare /activity) to the
// matching tab, keeping its query. A lazy chunk: only staff have these addresses.
export function ActivityRedirect({ from }: { from: LegacyActivityPage }) {
  const { search, hash } = useLocation();
  return <Navigate to={legacyActivityPath(from, search, hash)} replace />;
}
