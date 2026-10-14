import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";

export interface MenuItem {
  label: ReactNode;
  onSelect: () => void | Promise<unknown>;
  disabled?: boolean;
  tone?: "default" | "danger";
  /** A tooltip: what the item does, when the label alone doesn't say. */
  title?: string;
  /** Shown on the item while its onSelect promise is running; the menu stays open until it settles. */
  busyLabel?: ReactNode;
  /** Leave the menu open after choosing it: a setting that cycles in place (the theme). */
  keepOpen?: boolean;
}

export interface MenuProps {
  /** What the trigger shows (an avatar letter, "⋯", a label). */
  trigger: ReactNode;
  /** The trigger's accessible name, when its content isn't words. */
  label?: string;
  items: MenuItem[];
  align?: "left" | "right";
  /** Optional non-interactive header above the items (e.g. the signed-in name). */
  header?: ReactNode;
  triggerClassName?: string;
  triggerStyle?: CSSProperties;
  /** Extra classes on each item (e.g. a phone-sized tap target). */
  itemClassName?: string;
  /** The list's minimum width; narrower for a menu opened from a small card. */
  menuWidthClassName?: string;
  /**
   * Render the list in <body>, fixed beside the trigger (flipping above it near the
   * bottom of the screen), so a card with overflow-hidden or a scrolling table can't clip it.
   */
  portal?: boolean;
}

// Where a portalled list sits: fixed coordinates beside the trigger.
interface Placement { top: number; left: number }

// Menu is an accessible dropdown: a trigger with aria-haspopup/aria-expanded and a
// role=menu list. Arrow keys and Home/End move between items, Escape and a click
// outside close it, and focus returns to the trigger on close.
export function Menu({ trigger, label, items, align = "right", header, triggerClassName, triggerStyle, itemClassName = "", menuWidthClassName = "min-w-[200px]", portal = false }: MenuProps) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<number | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = useId();
  const [place, setPlace] = useState<Placement | null>(null);

  // A portalled list follows its trigger: below it, or above when there's no room below.
  useLayoutEffect(() => {
    if (!open || !portal) {
      setPlace(null);
      return;
    }
    const update = () => {
      const r = triggerRef.current?.getBoundingClientRect();
      if (!r) return;
      const h = listRef.current?.offsetHeight ?? 0;
      const w = listRef.current?.offsetWidth ?? 0;
      const below = window.innerHeight - r.bottom;
      const top = below < h + 8 && r.top > below ? Math.max(8, r.top - h - 8) : r.bottom + 8;
      // Lined up with the trigger's edge, but kept on screen: a phone is narrower than
      // the list is wide from some triggers.
      const want = align === "right" ? r.right - w : r.left;
      setPlace({ top, left: Math.max(8, Math.min(want, window.innerWidth - w - 8)) });
    };
    update();
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    return () => {
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
    };
  }, [open, portal, align]);

  const enabled = items.map((it, i) => (!it.disabled ? i : -1)).filter((i) => i >= 0);
  const focusItem = (i: number) => itemRefs.current[i]?.focus();

  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) triggerRef.current?.focus();
  };

  // Focus the first usable item on open, so the keyboard lands inside the menu.
  useEffect(() => {
    if (open && enabled.length) focusItem(enabled[0]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // A click anywhere outside closes it, without stealing focus back to the trigger.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (listRef.current?.contains(t)) return; // a portalled list isn't inside rootRef
      if (rootRef.current && !rootRef.current.contains(t)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const onMenuKey = (e: KeyboardEvent) => {
    if (enabled.length === 0) return;
    const at = enabled.indexOf(itemRefs.current.findIndex((el) => el === document.activeElement));
    const move = (to: number) => { e.preventDefault(); focusItem(enabled[(to + enabled.length) % enabled.length]); };
    switch (e.key) {
      case "ArrowDown": return move(at < 0 ? 0 : at + 1);
      case "ArrowUp": return move(at < 0 ? enabled.length - 1 : at - 1);
      case "Home": return move(0);
      case "End": return move(enabled.length - 1);
      case "Escape": e.preventDefault(); e.stopPropagation(); return close();
      case "Tab": return close(false);
    }
  };

  const wrap = (list: ReactNode) => (portal ? createPortal(list, document.body) : list);

  const pick = async (i: number) => {
    const it = items[i];
    if (it.disabled || busy !== null) return;
    const r = it.onSelect();
    if (r && typeof (r as Promise<unknown>).then === "function" && it.busyLabel) {
      setBusy(i);
      try { await r; } catch { /* the caller reports its own failure */ } finally { setBusy(null); }
    }
    if (!it.keepOpen) close();
  };

  return (
    <div ref={rootRef} className="relative">
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => { if ((e.key === "ArrowDown" || e.key === "ArrowUp") && !open) { e.preventDefault(); setOpen(true); } }}
        className={triggerClassName}
        style={triggerStyle}
      >
        {trigger}
      </button>
      {open && wrap(
        <div
          ref={listRef}
          id={menuId}
          role="menu"
          tabIndex={-1}
          aria-label={label}
          onKeyDown={onMenuKey}
          className={portal
            ? `fixed z-drawer ${menuWidthClassName} rounded-xl p-2`
            : `absolute z-drawer mt-2 ${menuWidthClassName} rounded-xl p-2 ${align === "right" ? "right-0" : "left-0"}`}
          style={{
            background: "var(--panel)", border: "1px solid var(--line)", boxShadow: "var(--shadow)",
            // Measured first (off-screen, still focusable), then placed beside the trigger
            // before the browser paints.
            ...(portal ? { top: place?.top ?? -9999, left: place?.left ?? -9999 } : {}),
          }}
        >
          {header && (
            <>
              <div className="truncate px-2.5 py-1.5 text-sm text-ink-dim">{header}</div>
              <div className="my-1 h-px" style={{ background: "var(--line)" }} />
            </>
          )}
          {items.map((it, i) => (
            <button
              key={i}
              ref={(el) => { itemRefs.current[i] = el; }}
              type="button"
              role="menuitem"
              tabIndex={-1}
              disabled={it.disabled}
              aria-disabled={it.disabled || undefined}
              title={it.title}
              onClick={() => pick(i)}
              className={`block w-full rounded-lg px-2.5 py-1.5 text-left text-base font-medium hover:bg-[var(--panel-2)] focus:bg-[var(--panel-2)] focus:outline-none disabled:opacity-50 ${itemClassName}`}
              style={{ color: it.tone === "danger" ? "var(--reject-text)" : "var(--ink)" }}
            >
              {busy === i && it.busyLabel ? it.busyLabel : it.label}
            </button>
          ))}
        </div>,
      )}
    </div>
  );
}
