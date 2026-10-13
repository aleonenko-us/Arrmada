import type { ProfileConflict } from "../../lib/api";

// ConflictNotes shows the target settings that work against each other under the row they
// belong to, in the avoid tone. They warn and never block Save: a setup that looks odd can
// still be what the owner meant.
export function ConflictNotes({ conflicts, row }: { conflicts: ProfileConflict[]; row: string }) {
  const mine = conflicts.filter((c) => c.row === row);
  if (mine.length === 0) return null;
  return (
    <div className="mt-1.5 flex flex-col gap-1" role="status">
      {mine.map((c) => (
        <div key={c.msg} className="flex items-start gap-1.5 rounded-lg px-2.5 py-1.5 text-xs leading-[1.45]"
          style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)", border: "1px solid var(--avoid)" }}>
          <span aria-hidden="true">⚠</span>
          <span>{c.msg}</span>
        </div>
      ))}
    </div>
  );
}
