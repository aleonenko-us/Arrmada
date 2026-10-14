import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { LINKS } from "../lib/links";
import { isAdmin, useMe } from "../lib/me";
import { useQuery } from "../lib/query";
import { versionBadge } from "../lib/updateStatus";

// The build changes only with a restart, and the check runs every 12 hours: one answer
// per page load is plenty. The Dashboard and System → Status share the admin one and
// refresh it themselves; Check now writes straight into it, so the dot follows at once.
const STALE_MS = 10 * 60_000;
// The Updates card on System → Status (its id is a deep-link target). Built from LINKS.status
// rather than a new LINKS entry, which every requester's first load would carry.
const UPDATES = `${LINKS.status}#updates`;

// VersionBadge is the quiet build line under the account row at the foot of the staff
// sidebar. Admins read it from System → Status's own answer (the update check rides along
// there) and it links to the Updates card, with a dot when the check found something
// newer; managers can't open that page, so theirs is the build from the public /status,
// as plain text. The row keeps its height while loading so nothing around it moves.
// onNavigate: following the link closes the sidebar when it is a drawer.
export function VersionBadge({ onNavigate }: { onNavigate?: () => void }) {
  const { user } = useMe();
  const admin = isAdmin(user);
  const { data: sys } = useQuery("system:status", api.systemStatus, { enabled: admin, staleMs: STALE_MS });
  const { data: pub } = useQuery("app:status", api.status, { enabled: !admin, staleMs: STALE_MS });
  const build = admin ? sys : pub;
  const badge = build ? versionBadge(build.version, build.commit, admin ? sys?.update : null) : null;
  const face = "-mx-1 inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md px-1 font-mono text-xs text-ink-faint";
  return (
    <div className="flex h-4 min-w-0 items-center" data-testid="version-badge">
      {badge && (admin ? (
        <Link to={UPDATES} onClick={onNavigate} title={badge.title} aria-label={badge.title} className={`${face} hover:text-ink-dim`}>
          <span className="truncate">{badge.label}</span>
          {badge.update && <span data-testid="update-dot" aria-hidden className="h-1.5 w-1.5 flex-none rounded-full" style={{ background: "var(--accent)" }} />}
        </Link>
      ) : (
        <span title={badge.title} className={face}><span className="sr-only">Arrmada </span><span className="truncate">{badge.label}</span></span>
      ))}
    </div>
  );
}
