import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Modal } from "../../ui/Modal";
import { useToast } from "../../ui";
import { useBackToClose } from "../../lib/useBackToClose";
import { useMe } from "../../lib/me";
import { api, type SystemStatus } from "../../lib/api";
import { signOut } from "../../lib/session";
import { searchApi } from "../../lib/searchApi";
import { paletteShortcut } from "../../lib/paletteOpen";
import {
  GROUP_LABEL, actionItems, highlight, isStaffRole, loadRecents, moveActive, pageItems, pushRecent, results, score,
  type DiscoverHit, type LibraryHit, type PaletteItem, type PaletteViewer,
} from "../../lib/palette";

// How long typing has to pause before asking the server. The library index is local to
// the server and cheap; Discover goes out to TMDB, so it waits a little longer.
const LIBRARY_DEBOUNCE_MS = 120;
const DISCOVER_DEBOUNCE_MS = 300;

// What came back for which query. A newer query keeps showing the last answer's rows that
// still match until its own arrives, so the list doesn't blink empty on every keystroke.
interface Answer<T> { q: string; hits: T[] }

// CommandPalette is the ⌘K / Ctrl K search: titles in the library (staff), pages and
// settings cards (by role), a few staff actions, and Discover. Up and Down move, Enter
// opens, Escape (or Back, on a phone) closes. A full-screen sheet on a phone, a card near
// the top of the window from 640px up. The default export is what PaletteHost lazy-loads.
export default function CommandPalette({ initialQuery = "", onClose }: { initialQuery?: string; onClose: () => void }) {
  const { user, external, booksEnabled, musicEnabled, metadataReady } = useMe();
  const navigate = useNavigate();
  const toast = useToast();
  const role = user?.role ?? "readonly";
  const viewer = useMemo<PaletteViewer>(() => ({ role, external, booksEnabled, musicEnabled }), [role, external, booksEnabled, musicEnabled]);
  const staff = isStaffRole(role);
  const pages = useMemo(() => pageItems(viewer), [viewer]);
  const actions = useMemo(() => actionItems(viewer), [viewer]);
  const [recents] = useState(() => (user ? loadRecents(user.id, viewer, [...pages, ...actions]) : []));

  const [query, setQuery] = useState(initialQuery);
  const q = query.trim();
  const [library, setLibrary] = useState<Answer<LibraryHit>>({ q: "", hits: [] });
  const [discover, setDiscover] = useState<Answer<DiscoverHit>>({ q: "", hits: [] });

  // Library titles: staff only (the server refuses anyone else anyway).
  useEffect(() => {
    if (!staff || !q) return;
    let live = true;
    const t = setTimeout(() => {
      searchApi.library(q)
        .then((hits) => { if (live) setLibrary({ q, hits }); })
        .catch(() => { if (live) setLibrary({ q, hits: [] }); });
    }, LIBRARY_DEBOUNCE_MS);
    return () => { live = false; clearTimeout(t); };
  }, [q, staff]);

  // Discover titles, through Discover's own search (which drops adult titles server-side).
  useEffect(() => {
    if (!metadataReady || q.length < 2) return;
    let live = true;
    const t = setTimeout(() => {
      api.discoverSearch(q)
        .then((r) => { if (live) setDiscover({ q, hits: r.items ?? [] }); })
        .catch(() => { if (live) setDiscover({ q, hits: [] }); });
    }, DISCOVER_DEBOUNCE_MS);
    return () => { live = false; clearTimeout(t); };
  }, [q, metadataReady]);

  const current = <T extends { title: string }>(a: Answer<T>): T[] =>
    !q ? [] : a.q === q ? a.hits : a.hits.filter((h) => score(q, h.title) > 0);
  const sections = results({
    query,
    viewer,
    pages,
    actions,
    library: current(library),
    discover: q.length >= 2 ? current(discover) : [],
    discoverReady: metadataReady,
    recents,
  });
  const flat = sections.flatMap((s) => s.items);

  // The highlighted row, by id so a list that grows under it (an answer arriving) keeps it.
  const [activeId, setActiveId] = useState<string | null>(null);
  const activeIndex = Math.max(0, flat.findIndex((i) => i.id === activeId));
  const active = flat[activeIndex];
  const baseId = useId();
  const optionId = (i: number) => `${baseId}-opt-${i}`;
  const listId = `${baseId}-list`;

  useEffect(() => {
    document.getElementById(optionId(activeIndex))?.scrollIntoView({ block: "nearest" });
    // optionId is derived from a stable id.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeIndex]);

  // Choosing closes first (stepping back over the sheet's history entry), then goes: the
  // destination replaces nothing and Back from it returns to where the palette was opened.
  const chosen = useRef<PaletteItem | null>(null);
  const done = () => {
    const item = chosen.current;
    chosen.current = null;
    onClose();
    if (item) run(item);
  };
  const close = useBackToClose(true, done);
  const choose = (item: PaletteItem | undefined) => {
    if (!item) return;
    if (user) pushRecent(user.id, item);
    chosen.current = item;
    close();
  };

  const run = (item: PaletteItem) => {
    if (item.action === "sign-out") { void signOut(); return; }
    if (item.to) navigate(item.to);
    if (item.action === "check-updates") void checkUpdates();
  };
  // Check for updates: the Updates card's own Check now, run from here; the card (opened
  // above) shows the answer and a toast says it.
  const checkUpdates = async () => {
    try {
      const [{ systemApi }, { updateHeadline }, { setQueryData }] = await Promise.all([import("../../lib/systemApi"), import("../../lib/updateStatus"), import("../../lib/query")]);
      const u = await systemApi.checkForUpdate();
      setQueryData<SystemStatus | undefined>("system:status", (old) => (old ? { ...old, update: u } : old));
      toast(updateHeadline(u).text);
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next = moveActive(activeIndex, flat.length, e.key);
      setActiveId(flat[next]?.id ?? null);
    } else if (e.key === "Enter") {
      e.preventDefault();
      choose(active);
    }
  };

  const inputRef = useRef<HTMLInputElement>(null);
  // Opened with a query (from the Settings search): the caret goes after it.
  useEffect(() => {
    const el = inputRef.current;
    if (el && initialQuery) el.setSelectionRange(initialQuery.length, initialQuery.length);
  }, [initialQuery]);

  let index = -1;
  return (
    <Modal
      open
      onClose={close}
      ariaLabel="Search"
      variant="sheet"
      scrim={0.55}
      initialFocus={inputRef}
      panelClassName="flex h-[100dvh] w-full flex-col overflow-hidden pt-[env(safe-area-inset-top)] sm:mt-[8vh] sm:h-auto sm:max-h-[min(620px,calc(100dvh-10vh-2rem))] sm:max-w-[640px] sm:self-start sm:rounded-2xl sm:pt-0"
      panelStyle={{ boxShadow: "var(--shadow)" }}
    >
      <div className="flex flex-none items-center gap-2.5 px-4 py-3" style={{ borderBottom: "1px solid var(--line)" }}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" className="flex-none text-ink-dim" aria-hidden>
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-expanded
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={flat.length ? optionId(activeIndex) : undefined}
          aria-label={staff ? "Search titles, pages and settings" : "Search pages and Discover"}
          placeholder={staff ? "Search titles, pages, settings…" : "Search Discover and pages…"}
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="go"
          value={query}
          onChange={(e) => { setQuery(e.target.value); setActiveId(null); }}
          onKeyDown={onKeyDown}
          className="min-h-[40px] min-w-0 flex-1 bg-transparent text-lg outline-none placeholder:text-ink-faint"
          style={{ color: "var(--ink)" }}
        />
        {/* A phone has no Escape key: a plain Cancel. Wider screens show the key. */}
        <button type="button" onClick={close} className="min-h-[40px] flex-none rounded-lg px-2 text-md font-semibold sm:hidden" style={{ color: "var(--accent-text)" }}>Cancel</button>
        <button type="button" onClick={close} aria-label="Close" className="hidden flex-none rounded px-1.5 font-mono text-xs text-ink-dim sm:block" style={{ border: "1px solid var(--line)" }}>Esc</button>
      </div>

      <div id={listId} role="listbox" aria-label="Results" className="thin-scroll min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] sm:pb-2">
        {flat.length === 0 && (
          <p className="m-0 px-3 py-6 text-center text-base text-ink-dim">{q ? `Nothing matches “${q}”.` : "Start typing to search."}</p>
        )}
        {sections.map((s) => (
          <div key={s.group} role="group" aria-labelledby={`${baseId}-${s.group}`} className="mb-1.5">
            <div id={`${baseId}-${s.group}`} role="presentation" className="px-2.5 pb-1 pt-2 font-mono text-2xs font-bold uppercase tracking-[0.12em] text-ink-dim">
              {GROUP_LABEL[s.group]}
            </div>
            {s.items.map((item) => {
              index += 1;
              const i = index;
              const on = i === activeIndex;
              return (
                <div
                  key={`${s.group}:${item.id}`}
                  id={optionId(i)}
                  role="option"
                  aria-selected={on}
                  tabIndex={-1}
                  onClick={() => choose(item)}
                  onKeyDown={(e) => { if (e.key === "Enter") choose(item); }}
                  onMouseMove={() => { if (!on) setActiveId(item.id); }}
                  className="flex min-h-[52px] cursor-pointer items-center gap-3 rounded-lg px-2.5 py-1.5 sm:min-h-[44px]"
                  style={on ? { background: "var(--accent-soft)" } : undefined}
                >
                  <Thumb item={item} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-md font-semibold" style={{ color: "var(--ink)" }}>
                      <Highlighted text={item.label} query={q} />
                    </span>
                    {item.hint && <span className="block truncate text-sm text-ink-dim">{item.hint}</span>}
                  </span>
                  {on && <span aria-hidden className="hidden flex-none font-mono text-xs text-ink-dim sm:block">Enter ↵</span>}
                </div>
              );
            })}
          </div>
        ))}
      </div>

      <div className="hidden flex-none items-center gap-4 px-4 py-2 text-xs text-ink-dim sm:flex" style={{ borderTop: "1px solid var(--line)" }}>
        <span>↑ ↓ to move</span>
        <span>Enter to open</span>
        <span>Esc to close</span>
        <span className="ml-auto">{paletteShortcut()} from any page</span>
      </div>
    </Modal>
  );
}

function Highlighted({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>;
  return (
    <>
      {highlight(text, query).map((p, i) =>
        p.hit ? <mark key={i} className="bg-transparent font-extrabold" style={{ color: "var(--accent-text)" }}>{p.text}</mark> : <span key={i}>{p.text}</span>,
      )}
    </>
  );
}

// Thumb is a row's picture: a poster for a title, otherwise a small mark for its group.
function Thumb({ item }: { item: PaletteItem }) {
  if (item.poster) {
    return <img src={smallPoster(item.poster)} alt="" loading="lazy" className="h-[42px] w-[28px] flex-none rounded object-cover" style={{ background: "var(--panel-2)" }} />;
  }
  const glyph: ReactNode = item.group === "actions"
    ? <path d="M13 3L5 14h6l-1 7 8-11h-6l1-7z" />
    : item.group === "discover"
      ? <><circle cx="12" cy="12" r="8" /><path d="M15 9l-1.8 4.2L9 15l1.8-4.2z" /></>
      : item.group === "library"
        ? <><rect x="4" y="5" width="16" height="14" rx="2" /><path d="M4 9h16M4 15h16" /></>
        : <><path d="M6 3h8l4 4v14H6z" /><path d="M14 3v4h4" /></>;
  return (
    <span className="grid h-[28px] w-[28px] flex-none place-items-center rounded-md text-ink-dim" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{glyph}</svg>
    </span>
  );
}

// smallPoster asks TMDB and Open Library for a thumbnail-sized image: a row's poster is
// 28px wide, so even TMDB's smallest (w92) is plenty on a retina screen.
function smallPoster(url: string): string {
  const tmdb = url.replace(/\/t\/p\/(?:w\d+|original)\//, "/t/p/w92/");
  if (tmdb !== url) return tmdb;
  return url.replace(/-L\.(jpg|jpeg|png)$/i, "-S.$1");
}
