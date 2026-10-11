import { useState } from "react";
import { Link } from "react-router-dom";
import { posterThumb } from "../../lib/img";
import type { LibraryConfig, LibraryItem } from "./types";

// Poster is the card art, or the title on a warm gradient when there's none.
export function Poster({ url, title }: { url?: string; title: string }) {
  if (url) return <img src={posterThumb(url)} alt={title} className="h-full w-full object-cover" loading="lazy" decoding="async" />;
  return (
    <div className="flex h-full w-full items-end p-2.5" style={{ background: "linear-gradient(150deg, hsl(24 40% 30%), hsl(20 35% 16%))" }}>
      <span className="text-[13px] font-bold text-white" style={{ textShadow: "0 1px 4px rgba(0,0,0,.6)" }}>{title}</span>
    </div>
  );
}

// CardHoverActions are the poster's mouse-over controls: Remove in the corner and, when
// there's something to look for, Search now along the foot (raised above an overlay bar).
export function CardHoverActions({ onRemove, onSearch, raised }: { onRemove: () => void; onSearch?: () => Promise<unknown>; raised?: boolean }) {
  const [searching, setSearching] = useState(false);
  const search = async () => {
    setSearching(true);
    try { await onSearch?.(); } finally { window.setTimeout(() => setSearching(false), 1500); }
  };
  return (
    <>
      <button onClick={onRemove} title="Remove" className="absolute right-1.5 top-1.5 hidden h-6 w-6 place-items-center rounded-full group-hover:grid" style={{ background: "rgba(20,12,7,.75)", color: "#fff" }}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none"><path d="M5 5l14 14M19 5L5 19" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" /></svg>
      </button>
      {onSearch && (
        <button onClick={search} disabled={searching} title="Search now" className={`absolute inset-x-1.5 ${raised ? "bottom-6" : "bottom-1.5"} hidden items-center justify-center gap-1.5 rounded-lg py-1.5 text-[11px] font-semibold group-hover:flex`} style={{ background: "rgba(20,12,7,.82)", color: "#fff" }}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none"><circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" /><path d="M20 20l-3.5-3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
          {searching ? "Searching…" : "Search now"}
        </button>
      )}
    </>
  );
}

function SelectTick({ on }: { on: boolean }) {
  return (
    <span className="absolute left-1.5 top-1.5 grid h-6 w-6 place-items-center rounded-full" style={{ background: on ? "var(--accent)" : "rgba(20,12,7,.65)", border: `1.5px solid ${on ? "var(--accent)" : "rgba(255,255,255,.6)"}` }}>
      {on && <svg width="13" height="13" viewBox="0 0 24 24" fill="none"><path d="M4 12l5 5L20 6" stroke="var(--accent-ink)" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" /></svg>}
    </span>
  );
}

// LibraryCard is one poster in a library grid. In select mode the whole card toggles the
// item; otherwise the poster and caption open it. The media-specific parts (caption,
// status word, the overlay along the poster's foot, hover controls) come from the config.
export function LibraryCard<T extends LibraryItem>({ item, config, selectable, selected, onToggle }: {
  item: T;
  config: LibraryConfig<T>;
  selectable: boolean;
  selected: boolean;
  onToggle: () => void;
}) {
  const st = config.card.status(item);
  const caption = (
    <>
      <div className="truncate text-[12.5px] font-semibold" title={item.title}>{item.title}</div>
      <div className="mt-1 flex items-center justify-between gap-2">
        <span className="min-w-0 truncate font-mono text-[10.5px] text-ink-faint">{config.card.meta(item)}</span>
        <span className="flex-none font-mono text-[9.5px] uppercase" style={{ color: st.color }} title={st.title}>{st.label}</span>
      </div>
    </>
  );
  const to = config.detailPath(item);
  return (
    <div className="group relative overflow-hidden rounded-xl" style={{ border: `1px solid ${selected ? "var(--accent)" : "var(--line)"}`, background: "var(--panel)", boxShadow: selected ? "0 0 0 1px var(--accent)" : "none" }}>
      <div className="relative" style={{ aspectRatio: "2/3" }}>
        {selectable ? (
          <button onClick={onToggle} aria-pressed={selected} className="block h-full w-full text-left">
            <Poster url={item.poster_url} title={item.title} />
            {selected && <div className="absolute inset-0" style={{ background: "var(--accent-soft)" }} />}
          </button>
        ) : (
          <Link to={to} className="block h-full w-full"><Poster url={item.poster_url} title={item.title} /></Link>
        )}
        {selectable && <SelectTick on={selected} />}
        {config.card.overlay?.(item)}
        {!selectable && config.card.hoverActions?.(item)}
      </div>
      {selectable ? (
        <button onClick={onToggle} className="block w-full p-2.5 text-left">{caption}</button>
      ) : (
        <Link to={to} className="block p-2.5">{caption}</Link>
      )}
    </div>
  );
}
