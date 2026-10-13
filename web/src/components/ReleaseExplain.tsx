// ReleaseExplain says why an eligible release ranks where it does, in the same few words
// everywhere a ranked list shows it (interactive search, the quality builder's samples and
// its real-title test): ✓ for each format it matched, ⚠ for each one it has that the profile
// avoids, a note when its preferences stopped counting because its bitrate collapsed, and
// the muted "ranked lower: …" line naming what it lost on against the release just above it.
// The chips wrap, so a row stays readable on a phone.

export function ReleaseExplain({ matched, avoided, waived, lostOn, className }: {
  matched?: string[] | null;
  avoided?: string[] | null;
  waived?: boolean;
  lostOn?: string;
  className?: string;
}) {
  const m = matched ?? [];
  const a = avoided ?? [];
  if (m.length === 0 && a.length === 0 && !waived && !lostOn) return null;
  return (
    <div className={className}>
      {(m.length > 0 || a.length > 0 || waived) && (
        <div className="flex flex-wrap gap-1">
          {m.map((f) => (
            <span key={`m-${f}`} className="rounded-md px-1.5 py-0.5 font-mono text-xs font-semibold" style={{ background: "var(--good-soft)", color: "var(--good-text)" }}>
              ✓ {f}
            </span>
          ))}
          {a.map((f) => (
            <span key={`a-${f}`} className="rounded-md px-1.5 py-0.5 font-mono text-xs font-semibold" style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }}
              title="A format this profile avoids: grabbed only when nothing without it is available">
              ⚠ {avoidedLabel(f)}
            </span>
          ))}
          {waived && (
            <span className="rounded-md px-1.5 py-0.5 text-xs" style={{ background: "var(--panel-2)", border: "1px solid var(--line-soft)", color: "var(--ink-dim)" }}
              title="Its bitrate is under a third of the best release on offer, so the formats you prefer don't count for it">
              preference ignored — bitrate too low
            </span>
          )}
        </div>
      )}
      {lostOn && (
        <div className="mt-1 text-xs text-ink-faint" title="Compared with the release ranked just above it in Best match order">
          ranked lower: {lostOn}
        </div>
      )}
    </div>
  );
}

// avoidedLabel reads an avoided format as a chip: "Dolby Vision (avoided)". The bitrate floor
// already reads as a phrase ("a bitrate under your 5 Mbps floor") and is shown as it is.
function avoidedLabel(f: string): string {
  return f.startsWith("a ") ? f : `${f} (avoided)`;
}
