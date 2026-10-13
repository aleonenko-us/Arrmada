import { useRef, type ReactNode } from "react";
import { IconButton } from "../../ui";

// A rail is a sideways-scrolling strip of posters: Discover's rows, the requests strip
// and the Books tab's rows. The arrows page it by most of its width.

export interface Rail {
  ref: React.RefObject<HTMLDivElement>;
  scroll: (dir: -1 | 1) => void;
}

export function useRail(): Rail {
  const ref = useRef<HTMLDivElement>(null);
  const scroll = (dir: -1 | 1) => ref.current?.scrollBy({ left: dir * Math.max(600, ref.current.clientWidth * 0.8), behavior: "smooth" });
  return { ref, scroll };
}

export function RailStrip({ rail, children }: { rail: Rail; children: ReactNode }) {
  return <div ref={rail.ref} className="thin-scroll flex gap-3 overflow-x-auto pb-2" style={{ scrollSnapType: "x proximity" }}>{children}</div>;
}

// RailArrows are the left/right buttons; Discover's hide on a phone, where a swipe does it.
export function RailArrows({ rail, className = "hidden gap-1 sm:flex" }: { rail: Rail; className?: string }) {
  return (
    <div className={className}>
      <ArrowBtn dir={-1} onClick={() => rail.scroll(-1)} />
      <ArrowBtn dir={1} onClick={() => rail.scroll(1)} />
    </div>
  );
}

export function ArrowBtn({ dir, onClick }: { dir: -1 | 1; onClick: () => void }) {
  return (
    <IconButton label={dir === -1 ? "Scroll left" : "Scroll right"} onClick={onClick} className="h-7 w-7 rounded-full" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden style={{ transform: dir === -1 ? "rotate(180deg)" : "none" }}><path d="M9 6l6 6-6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
    </IconButton>
  );
}
