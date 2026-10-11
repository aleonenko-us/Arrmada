import { Suspense, useEffect, useRef } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { FleetMark } from "./FleetMark";
import { ErrorBoundary } from "./ErrorBoundary";
import { PageSkeleton } from "./PageSkeleton";
import { NotificationBell } from "./NotificationBell";
import { BottomTabs } from "./BottomTabs";
import { PushPromptHost } from "./PushPromptHost";
import { PlayerHost } from "../lib/playerStub";
import { useMe } from "../lib/me";
import { signOut } from "../lib/session";
import { useDocumentTitle } from "../lib/title";
import { requesterNav, sectionFor } from "../lib/requesterNav";
import { Menu } from "../ui/Menu";
import { useScrollMemory } from "../lib/useScrollMemory";

// UserLayout is the requester-facing shell, and what installs as the PWA on phones. Wider
// screens get a slim branded top bar with the places inline; a phone gets the mark, the
// section's name and the bell on top, and the places as tabs along the bottom.
export function UserLayout() {
  const { user, external, booksEnabled } = useMe();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  useDocumentTitle();
  // The page scrolls inside <main>: Back puts it back where it was, a link opens at the top.
  const mainRef = useRef<HTMLElement>(null);
  useScrollMemory(mainRef);
  // Outside sessions get no Calendar (the /calendar route isn't mounted or allowlisted
  // for them), so no link that silently bounces. The shelf is the exception: its two
  // endpoints are allowlisted, so a requester can pick up an ebook from anywhere.
  const nav = requesterNav({ external, booksEnabled });
  const section = sectionFor(nav, pathname);

  // The tab bar's height only counts while this shell is up (index.css, --tabbar-h).
  useEffect(() => {
    const root = document.documentElement;
    root.classList.add("has-tabbar");
    return () => root.classList.remove("has-tabbar");
  }, []);

  return (
    <div className="flex h-full flex-col font-sans">
      <header className="pt-safe px-safe flex items-center justify-between gap-2 pb-2.5" style={{ borderBottom: "1px solid var(--line)", background: "var(--sidebar)" }}>
        <div className="flex min-w-0 flex-none items-center gap-2.5">
          <span className="grid h-[28px] w-[28px] flex-none place-items-center rounded-lg" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
            <FleetMark className="h-[16px] w-[16px]" />
          </span>
          <span className="hidden text-[15px] font-extrabold tracking-[0.12em] md:inline">ARRMADA</span>
          {/* A phone has no inline links, so the header says where you are instead. */}
          {section && <span className="truncate text-[15px] font-bold sm:hidden">{section.label}</span>}
        </div>
        <nav aria-label="Sections" className="thin-scroll hidden min-w-0 items-center gap-1 overflow-x-auto whitespace-nowrap sm:flex">
          {nav.filter((n) => n.header).map((n) => (
            <NavLink key={n.key} to={n.to} className="flex-none rounded-lg px-3 py-1.5 text-[12.5px] font-semibold" style={({ isActive }) => ({ background: isActive ? "var(--accent-soft)" : "transparent", color: isActive ? "var(--accent)" : "var(--ink-dim)" })}>{n.label}</NavLink>
          ))}
        </nav>
        <div className="flex flex-none items-center gap-2">
          <NotificationBell />
          {/* The kit's menu: arrow keys, Escape and focus back to the avatar. */}
          <Menu
            label="Account menu"
            trigger={(user?.username?.[0] ?? "?").toUpperCase()}
            header={<span title={user?.username}>{user?.username || "Guest"}</span>}
            triggerClassName="grid h-9 w-9 place-items-center rounded-full text-[12px] font-bold"
            triggerStyle={{ background: "var(--accent-soft)", color: "var(--accent)", border: "1px solid var(--accent-line)" }}
            itemClassName="min-h-[44px]"
            items={[
              { label: "Me & notifications", onSelect: () => navigate("/me") },
              { label: "Password & devices", onSelect: () => navigate("/me#account") },
              { label: "Audiobook apps & password", onSelect: () => navigate("/audiobooks?tab=apps") },
              { label: "Sign out", tone: "danger", onSelect: signOut },
            ]}
          />
        </div>
      </header>
      {/* pb-chrome: the last row of a page scrolls clear of the phone tab bar. */}
      <main ref={mainRef} className="pb-chrome min-w-0 flex-1 overflow-y-auto">
        {/* Keyed by path so navigating away from a broken page recovers. */}
        <ErrorBoundary resetKey={pathname}>
          <Suspense fallback={<PageSkeleton />}>
            <Outlet />
          </Suspense>
        </ErrorBoundary>
      </main>
      <BottomTabs items={nav.filter((n) => n.tab)} />
      {/* The audiobook mini-player, above the tab bar; it outlives every page. */}
      <PlayerHost />
      {/* "Get notified when it's ready?" once, right after a first request. */}
      <PushPromptHost />
    </div>
  );
}
