// Scrim is the dim, click-to-close layer under a drawer that slides over the page (the
// sidebar on a narrow window). Unlike Modal it holds nothing: the drawer beside it owns
// focus and Escape, and the scrim only dims the page and closes on a click.
export function Scrim({ onClose, className }: { onClose: () => void; className?: string }) {
  return <div aria-hidden className={`fixed inset-0 bg-black/50 ${className ?? ""}`} onClick={onClose} />;
}
