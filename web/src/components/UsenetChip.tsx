// UsenetChip marks a release, download or event that came over Usenet. Only usenet rows
// carry it: torrents are the usual case and stay unmarked, so the chip says something.
export function UsenetChip({ className = "" }: { className?: string }) {
  return (
    <span
      className={`rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase ${className}`}
      title="Downloaded from Usenet through SABnzbd"
      data-testid="usenet-chip"
      style={{ background: "var(--panel-2)", color: "var(--ink-dim)", border: "1px solid var(--line)" }}
    >
      Usenet
    </span>
  );
}
