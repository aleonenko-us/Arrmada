import { Suspense, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { lazyPage } from "../lib/lazyPage";
import { useMe } from "../lib/me";

// Only an admin on a fresh install ever sees the wizard, so it is its own chunk.
const SetupWizard = lazyPage(() => import("../pages/SetupWizard"), "SetupWizard");

const loading = <div className="grid h-full place-items-center text-[13px] text-ink-dim">Loading…</div>;

// SetupGate shows the first-run wizard to an admin until setup is finished or skipped.
// Everyone else, and an admin on a configured install, goes straight to the app. A
// failed check never blocks the app — it just shows the app. It sits inside the router
// (around the staff console's frame), so the wizard's links and the Plex sign-in can use it.
export function SetupGate({ children }: { children: React.ReactNode }) {
  const { user } = useMe();
  const admin = user?.role === "admin";
  const [needed, setNeeded] = useState<boolean | null>(admin ? null : false);
  const navigate = useNavigate();

  useEffect(() => {
    if (!admin) return;
    api.setupState().then((s) => setNeeded(s.needed)).catch(() => setNeeded(false));
  }, [admin]);

  if (needed === null) return loading;
  if (needed) {
    return (
      <Suspense fallback={loading}>
        {/* to: a step on the last page's checklist, opened once setup is finished. */}
        <SetupWizard onDone={(to) => { setNeeded(false); if (to) navigate(to); }} />
      </Suspense>
    );
  }
  return <>{children}</>;
}
