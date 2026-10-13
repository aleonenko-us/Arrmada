import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { ActivityDownload, TransferItem } from "../../lib/api";
import { posterThumb } from "../../lib/img";
import { Menu, type MenuItem } from "../../ui";

// The parts every transfer card on the Activity page shares: the title's poster and name
// (linked to its page) above the release name, the chips that say what part of the title
// it covers and who asked for it, and an action row that folds into the ⋯ menu on a phone.

/** Where a transfer's title lives in the app. */
export function transferHref(item: Pick<TransferItem, "kind" | "id">): string {
  switch (item.kind) {
    case "series": return `/series/${item.id}`;
    case "book": return `/books/${item.id}`;
    case "music": return `/music/album/${item.id}`;
    default: return `/movies/${item.id}`;
  }
}

/** "for Mum", "for Mum and Dad", "for Mum, Dad +2". */
export function requestedFor(names: string[] | undefined): string {
  if (!names || names.length === 0) return "";
  if (names.length === 1) return `for ${names[0]}`;
  if (names.length === 2) return `for ${names[0]} and ${names[1]}`;
  return `for ${names[0]}, ${names[1]} +${names.length - 2}`;
}

// Tailwind's sm breakpoint: below it, a card keeps only its main action on the row.
const WIDE = "(min-width: 640px)";

function wideNow(): boolean {
  try { return typeof window !== "undefined" && window.matchMedia(WIDE).matches; } catch { return true; }
}

/** Whether the viewport is at least 640 px wide; follows resizes. */
export function useWide(): boolean {
  const [wide, setWide] = useState(wideNow);
  useEffect(() => {
    let mq: MediaQueryList;
    try { mq = window.matchMedia(WIDE); } catch { return; }
    const onChange = () => setWide(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return wide;
}

const KIND_LETTER: Record<string, string> = { movie: "M", series: "TV", book: "B", music: "♪" };

/** A 40×60 poster: the title's cover when there is one, else a quiet tile with its kind. */
export function TransferPoster({ it }: { it: ActivityDownload }) {
  const [broken, setBroken] = useState(false);
  const src = posterThumb(it.item?.poster_url);
  const box = "h-[60px] w-[40px] flex-none overflow-hidden rounded-md";
  if (src && !broken) {
    return <img src={src} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} className={`${box} object-cover`} style={{ background: "var(--panel-2)" }} />;
  }
  return (
    <div aria-hidden className={`${box} flex items-center justify-center font-mono text-xs font-bold`} style={{ background: "var(--panel-2)", color: "var(--ink-faint)", border: "1px solid var(--line-soft)" }}>
      {KIND_LETTER[it.item?.kind ?? it.media_type ?? "movie"] ?? "?"}
    </div>
  );
}

/**
 * The card's heading: the title, linked to its page, over the release name in small
 * monospace — or, for a torrent no grab knows, the release name alone.
 */
export function TransferTitle({ it }: { it: ActivityDownload }) {
  if (!it.item) {
    return <div className="truncate font-mono text-sm" title={it.name} data-testid="transfer-title">{it.name}</div>;
  }
  const { title, year, byline } = it.item;
  return (
    <>
      <div className="flex min-w-0 items-baseline gap-1.5">
        <Link to={transferHref(it.item)} className="truncate text-md font-semibold hover:underline" style={{ color: "var(--ink)" }} data-testid="transfer-title">
          {title}
        </Link>
        {year ? <span className="flex-none font-mono text-xs text-ink-faint">{year}</span> : null}
        {byline && <span className="hidden truncate text-xs text-ink-faint sm:inline">· {byline}</span>}
      </div>
      <div className="truncate font-mono text-xs text-ink-faint" title={it.name} data-testid="transfer-release">{it.name}</div>
    </>
  );
}

function Chip({ children, fg, bg, title, testId }: { children: ReactNode; fg: string; bg: string; title?: string; testId?: string }) {
  return <span className="rounded px-1.5 py-0.5 font-mono text-2xs uppercase" title={title} data-testid={testId} style={{ background: bg, color: fg }}>{children}</span>;
}

/** What part of the title the torrent covers, whether it's an upgrade, and who asked for it. */
export function ItemChips({ it }: { it: ActivityDownload }) {
  const item = it.item;
  if (!item) {
    // Nothing in Arrmada grabbed it: say so once, quietly (the seeding view's stage chip
    // already does for finished ones).
    return it.managed || it.stage === "not_managed" ? null : <Chip fg="var(--ink-faint)" bg="var(--panel-2)">Not managed</Chip>;
  }
  const who = requestedFor(item.requested_by);
  return (
    <>
      {item.scope && <Chip fg="var(--accent)" bg="var(--accent-soft)" testId="transfer-scope">{item.scope}</Chip>}
      {item.upgrade && <Chip fg="var(--good)" bg="var(--panel-2)" title="Replaces the file it already has">Upgrade</Chip>}
      {who && (
        <span className="max-w-[160px] truncate rounded-full px-2 py-0.5 text-xs font-semibold" title={`Requested by ${item.requested_by?.join(", ")}`} data-testid="transfer-requester" style={{ background: "var(--panel-2)", color: "var(--ink-dim)", border: "1px solid var(--line)" }}>
          {who}
        </span>
      )}
    </>
  );
}

/** A secondary action: a button on a wide screen, a ⋯ menu item on a phone. */
export interface CardAction {
  label: string;
  onSelect: () => void | Promise<unknown>;
  disabled?: boolean;
  /** Destroys something (files, the torrent): red, and the danger tone in the menu. */
  danger?: boolean;
  /** The button's colour when it isn't danger (Block is the avoid tone). */
  tone?: string;
  title?: string;
}

/**
 * The card's actions. The primary one always sits on the row; the secondary ones do too
 * on a wide screen, and join the ⋯ menu (with the extras) below 640 px, so a phone keeps
 * the title readable instead of squeezing it behind five buttons.
 */
export function CardActions({ name, primary, secondary = [], extras = [] }: { name: string; primary?: ReactNode; secondary?: CardAction[]; extras?: MenuItem[] }) {
  const wide = useWide();
  const asItem = (a: CardAction): MenuItem => ({ label: a.label, onSelect: a.onSelect, disabled: a.disabled, tone: a.danger ? "danger" : "default" });
  const menu = wide ? extras : [...secondary.map(asItem), ...extras];
  return (
    <div className="flex flex-none items-center gap-1">
      {primary}
      {wide && secondary.map((a) => (
        <button key={a.label} onClick={() => void a.onSelect()} disabled={a.disabled} title={a.title ?? a.label} className="rounded-lg px-2 py-1.5 text-xs font-semibold transition-colors" style={{ border: `1px solid ${a.danger ? "var(--reject)" : a.tone ?? "var(--line)"}`, color: a.danger ? "var(--reject)" : a.tone ?? "var(--ink-dim)", opacity: a.disabled ? 0.5 : 1 }}>
          {a.label}
        </button>
      ))}
      {menu.length > 0 && (
        <Menu
          trigger="⋯"
          label={`More actions for ${name}`}
          triggerClassName="rounded-md px-2 py-1 text-xs font-semibold"
          triggerStyle={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
          itemClassName={wide ? "" : "min-h-[44px]"}
          items={menu}
        />
      )}
    </div>
  );
}
