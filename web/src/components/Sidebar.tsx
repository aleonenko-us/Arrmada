import { lazy, Suspense, useCallback, useEffect, useState, type ReactNode, type RefObject } from "react";
import { NavLink } from "react-router-dom";
import { visibleNav } from "../lib/nav";
import { useMe, isAdmin } from "../lib/me";
import { api } from "../lib/api";
import { badgeFor, useAttention, type NavBadge } from "../lib/useAttention";
import { FleetMark } from "./FleetMark";
import { Icon } from "./icons";
import { NotificationBell } from "./NotificationBell";
import { nextTheme, THEME_LABEL, useTheme, type ThemePref } from "../lib/theme";
import { Scrim } from "../ui/Scrim";
import { VersionBadge } from "./VersionBadge";
import { SearchBar } from "./PaletteHost";
import { signOut as endSession } from "../lib/session";

const PlexAccountLink = lazy(() => import("./PlexAccountLink"));
const JellyfinAccountLink = lazy(() => import("./JellyfinAccountLink"));

const PILL_TONE: Record<NavBadge["tone"], { background: string; color: string }> = {
  accent: { background: "var(--accent-soft)", color: "var(--accent-text)" },
  reject: { background: "var(--reject-soft)", color: "var(--reject-text)" },
  avoid: { background: "var(--avoid-soft)", color: "var(--avoid-text)" },
};

// CountPill is a sidebar entry's live count; nothing at zero.
function CountPill({ badge }: { badge: NavBadge | null }) {
  if (!badge) return null;
  return (
    <>
      <span
        className="ml-auto min-w-[18px] rounded-full px-1.5 py-px text-center font-mono text-xs font-semibold"
        style={PILL_TONE[badge.tone]}
        title={badge.label}
        aria-hidden
      >
        {badge.count > 99 ? "99+" : badge.count}
      </span>
      <span className="sr-only">, {badge.label}</span>
    </>
  );
}

// The theme button's face shows the choice in force: a monitor for System (follow the
// device), a sun for Light, a moon for Dark. Clicking moves to the next one.
const THEME_ICON: Record<ThemePref, ReactNode> = {
  system: (
    <>
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M8 20h8M12 16v4" />
    </>
  ),
  light: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </>
  ),
  dark: <path d="M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z" />,
};

function ThemeButton() {
  const [pref, setPref] = useTheme();
  const label = `Theme: ${THEME_LABEL[pref]}`;
  return (
    <button
      type="button"
      onClick={() => setPref(nextTheme(pref))}
      title={`${label} (click for ${THEME_LABEL[nextTheme(pref)]})`}
      aria-label={label}
      className="ml-auto grid h-[30px] w-[30px] flex-none place-items-center rounded-lg"
      style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        {THEME_ICON[pref]}
      </svg>
    </button>
  );
}

// bell: this screen is wide enough that the sidebar is always open, so the inbox bell
// lives in its brand row (AppLayout's compact bar has it otherwise).
export function Sidebar({ open, onClose, bell = false, returnFocus }: { open: boolean; onClose: () => void; bell?: boolean; returnFocus?: RefObject<HTMLElement> }) {
  const { user, booksEnabled, musicEnabled } = useMe();
  // As a drawer: Escape or the backdrop closes it and puts focus back on the menu button
  // that opened it (a link closes it too, but focus then belongs to the new page).
  const dismiss = useCallback(() => {
    onClose();
    returnFocus?.current?.focus();
  }, [onClose, returnFocus]);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") dismiss(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, dismiss]);
  // Count pills from the Needs-you feed (the staff layout polls it; this only reads).
  const attention = useAttention();
  // A dot beside Audiobooks: green when the server is running, red when it's switched on
  // but couldn't start, nothing when it's off.
  const [audioDot, setAudioDot] = useState<string | null>(null);
  useEffect(() => {
    api.myAudio()
      .then((a) => setAudioDot(a.enabled ? (a.running ? "var(--good)" : "var(--reject)") : null))
      .catch(() => {});
  }, []);
  const signOut = () => { void endSession(user?.id); };
  // Hide nav entries for modules an admin has turned off, and admin-only pages (Logs)
  // from managers.
  const nav = visibleNav({ admin: isAdmin(user), booksEnabled, musicEnabled });
  return (
    <>
      {/* Dim + click-to-close backdrop, only while the drawer is open on narrow windows. */}
      {open && <Scrim onClose={dismiss} className="z-40 lg:hidden" />}

      <aside
        className={`fixed inset-y-0 left-0 z-50 flex w-[236px] flex-none transform flex-col bg-sidebar transition-transform duration-200 lg:static lg:z-auto lg:translate-x-0 ${
          open ? "translate-x-0" : "-translate-x-full"
        }`}
        // As a drawer in the installed iPhone app it runs edge to edge: keep the brand row
        // below the status bar and the foot above the home indicator. Only the nav list
        // scrolls: the brand row and the foot (you, and which build this is) stay put. The
        // nav is relative so its screen-reader-only labels (absolutely placed) belong to its
        // scroll box; otherwise the transformed aside holds them and grows a second scroll.
        style={{ borderRight: "1px solid var(--line)", paddingTop: "env(safe-area-inset-top, 0px)", paddingBottom: "env(safe-area-inset-bottom, 0px)" }}
      >
        <div className="flex flex-none items-center gap-2.5 px-[18px] pb-3 pt-[18px]">
          <span
            className="grid h-[30px] w-[30px] place-items-center rounded-[9px]"
            style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}
          >
            <FleetMark className="h-[18px] w-[18px]" />
          </span>
          <span className="text-lg font-extrabold tracking-[0.12em]">ARRMADA</span>
          {bell && <div className="ml-auto"><NotificationBell placement="side" /></div>}
        </div>
        {/* Search everything (⌘K / Ctrl K): titles, pages, settings and a few actions. */}
        <div className="flex-none px-2.5 pb-1">
          {/* As a drawer it closes first: the palette takes over the screen. */}
          <SearchBar onOpen={onClose} />
        </div>

        <nav className="relative flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto overflow-x-hidden px-2.5 pb-3 pt-2">
          {nav.map((group, gi) => (
            <div key={gi} className="flex flex-col gap-0.5">
              {group.group && (
                <div className="px-2.5 pb-1.5 pt-3.5 font-mono text-2xs font-bold uppercase tracking-[0.12em] text-ink-faint">
                  {group.group}
                </div>
              )}
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.end}
                  onClick={onClose}
                  className="flex w-full items-center gap-2.5 rounded-[9px] px-2.5 py-2 text-left text-md transition-colors"
                  style={({ isActive }) =>
                    isActive
                      ? { background: "var(--accent-soft)", color: "var(--accent-text)", fontWeight: 600 }
                      : { color: "var(--ink-dim)" }
                  }
                >
                  <Icon name={item.icon} className="flex-none" />
                  {item.label}
                  {item.tag && (
                    <span
                      className="ml-auto rounded-full px-1.5 py-px font-mono text-2xs uppercase"
                      style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }}
                    >
                      {item.tag}
                    </span>
                  )}
                  {item.status === "audiobook-server" && audioDot && (
                    <span className="ml-auto h-1.5 w-1.5 rounded-full" style={{ background: audioDot }} title={audioDot === "var(--good)" ? "Running" : "Switched on but not running"} />
                  )}
                  {item.badge && <CountPill badge={badgeFor(item.badge, attention?.counts)} />}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        {/* The foot, pinned under the nav: you, the theme and sign out, and under them which
            build this is (with a dot when the update check found a newer one). */}
        <div className="flex-none" style={{ borderTop: "1px solid var(--line)" }}>
          <div className="flex flex-col gap-0.5 px-3.5 pb-2 pt-3.5">
            <div className="flex items-center gap-2.5">
              {/* You: notifications, password and signed-in devices live on the Me page. */}
              <NavLink to="/me" onClick={onClose} aria-label={`${user?.username ?? "Your account"} — Me`} className="flex min-w-0 items-center gap-2.5 rounded-lg">
                <span className="grid h-[30px] w-[30px] flex-none place-items-center rounded-full bg-[#5f5142] text-sm font-bold text-white">
                  {(user?.username?.[0] ?? "?").toUpperCase()}
                </span>
                <div className="min-w-0">
                  <div className="truncate text-base font-semibold" title={user?.username}>{user?.username ?? "…"}</div>
                  <div className="font-mono text-xs capitalize text-ink-faint">{user?.role ?? ""}</div>
                </div>
              </NavLink>
              <ThemeButton />
              <button
                onClick={signOut}
                title="Sign out"
                aria-label="Sign out"
                className="grid h-[30px] w-[30px] flex-none place-items-center rounded-lg"
                style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--reject-text)" }}
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4M16 17l5-5-5-5M21 12H9" />
                </svg>
              </button>
            </div>
            <VersionBadge onNavigate={onClose} />
          </div>
          {/* Your own Plex link (Discover's recommendations use its watch history), and your
              Jellyfin link while a Jellyfin server is connected or you're linked to one. */}
          <Suspense fallback={null}><PlexAccountLink variant="sidebar" /></Suspense>
          <Suspense fallback={null}><JellyfinAccountLink variant="sidebar" /></Suspense>
        </div>
      </aside>
    </>
  );
}
