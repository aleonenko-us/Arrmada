// The Arrmada mark (the "A-sail"): two sails back to back form an "A" over a hull.
// Same paths as web/public/icon.svg, framed tight on the mark; drawn in currentColor.
export function FleetMark({ className }: { className?: string }) {
  return (
    <svg viewBox="72 72 368 368" fill="none" className={className} aria-hidden>
      <path d="M244 92 L244 352 L128 352 Q176 212 244 92 Z" fill="currentColor" opacity="0.72" />
      <path d="M268 92 L268 352 L384 352 Q336 212 268 92 Z" fill="currentColor" />
      <path d="M128 376 L384 376 Q368 420 330 420 L182 420 Q144 420 128 376 Z" fill="currentColor" />
    </svg>
  );
}
