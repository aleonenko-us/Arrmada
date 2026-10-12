import { useState } from "react";
import { req, type ConvertHistoryEntry, type ConvertHoldStats } from "../../lib/api";
import { convertApi } from "../../lib/convertApi";
import { fmtBytes } from "../../lib/disposal";
import { useConfirm } from "../../ui";

// Convert's originals hold, on the Convert page: what it keeps for undo (the Overview's
// Space saved card), and the Release-now control on a held ledger row. A converted file's
// original waits in the hold for the undo window; space only counts as saved once it's
// released — by its window ending, or by you here.

// inDays says when a unix time comes: "today", "tomorrow", "in 3 days".
export function inDays(unix?: number): string {
  if (!unix) return "";
  const d = Math.ceil((unix * 1000 - Date.now()) / 86_400_000);
  return d <= 0 ? "today" : d === 1 ? "tomorrow" : `in ${d} days`;
}

// The hold's actions. They live here, in the Convert page's own chunk, not in lib/api.ts,
// which every page (the requester's first load included) pays for.
const holdApi = {
  // Deletes a held original for good (its conversion can't be undone after).
  release: (id: number) => req<ConvertHistoryEntry>(`/api/v1/convert/history/${id}/release`, { method: "POST" }),
  releaseAll: (olderThanDays = 0) =>
    req<{ released: number; freed_bytes: number }>(`/api/v1/convert/history/release?older_than_days=${olderThanDays}`, { method: "POST" }),
  // Puts a conversion's original back from the hold in place of the converted file.
  revert: (id: number) => req<ConvertHistoryEntry>(`/api/v1/convert/history/${id}/revert`, { method: "POST" }),
};

const ghostBtn = { border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" } as const;

// HeldSummary is the held half of Space saved: what's held so it can be undone, when the
// next original goes, a meter against the hold's budget, and Release all.
export function HeldSummary({ hold, flash, onChanged }: { hold?: ConvertHoldStats; flash: (m: string) => void; onChanged: () => void }) {
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  if (!hold) return null;
  if (hold.held_count === 0) {
    return (
      <div className="mt-1.5 text-[11.5px] text-ink-faint">
        {hold.hold_days > 0 ? `Nothing held — each original is kept for ${hold.hold_days} day${hold.hold_days === 1 ? "" : "s"} after its conversion so it can be undone` : "Originals are deleted once verified — there is no undo"}
      </div>
    );
  }
  const pct = hold.budget_bytes > 0 ? Math.min(100, Math.round((hold.held_bytes / hold.budget_bytes) * 100)) : 0;
  const releaseAll = async () => {
    const ok = await confirm({
      title: `Release ${hold.held_count === 1 ? "the held original" : `all ${hold.held_count} held originals`}?`,
      body: `Deletes ${fmtBytes(hold.held_bytes)} of originals for good, freeing about ${fmtBytes(hold.held_saving_bytes)}. Those conversions can't be undone after this.`,
      confirmLabel: "Release",
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const r = await holdApi.releaseAll(0);
      flash(`Released ${r.released} original${r.released === 1 ? "" : "s"} — freed ${fmtBytes(r.freed_bytes)}`);
      onChanged();
    } catch (e) { flash((e as Error).message); } finally { setBusy(false); }
  };
  return (
    <div className="mt-1.5 text-[11.5px] text-ink-dim">
      <div>
        <b style={{ color: "var(--ink)" }}>{fmtBytes(hold.held_bytes)}</b> held so you can undo · {hold.held_count.toLocaleString()} file{hold.held_count === 1 ? "" : "s"}
        {hold.next_release_at ? ` · next release ${inDays(hold.next_release_at)}` : ""}
        <span className="text-ink-faint"> — frees ~{fmtBytes(hold.held_saving_bytes)} when released</span>
      </div>
      {hold.budget_bytes > 0 && (
        <div className="mt-1.5 flex items-center gap-2">
          <div className="h-1.5 flex-1 overflow-hidden rounded" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}
            role="meter" aria-label="Originals hold used" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
            <div className="h-full" style={{ width: `${pct}%`, background: pct >= 90 ? "var(--avoid)" : "var(--accent)" }} />
          </div>
          <span className="font-mono text-[10.5px] text-ink-faint">{pct}% of {fmtBytes(hold.budget_bytes)}</span>
        </div>
      )}
      <button onClick={releaseAll} disabled={busy} className="mt-1.5 rounded px-2 py-0.5 text-[10.5px] font-semibold disabled:opacity-50" style={ghostBtn}>
        {busy ? "Releasing…" : "Release all…"}
      </button>
    </div>
  );
}

// holdLine says where a ledger row's original is: "held · 11 days left", "released", …
export function holdLine(e: ConvertHistoryEntry): string {
  switch (e.hold_state) {
    case "held": {
      const left = e.hold_until ? Math.max(0, Math.ceil((e.hold_until * 1000 - Date.now()) / 86_400_000)) : 0;
      return left > 0 ? `original held · ${left} day${left === 1 ? "" : "s"} left` : "original held · released within the hour";
    }
    case "released": return "original released";
    case "reverted": return "reverted — the original is back";
    case "missing": return "the held original had gone";
    default: return "";
  }
}

// revertBody says exactly what a revert will do, from the ledger row.
function revertBody(e: ConvertHistoryEntry): string {
  const was = (e.src_spec ?? "").split(" · ")[0];
  const now = (e.codec || (e.out_spec ?? "").split(" · ")[0]).toUpperCase();
  const what = e.kind === "episode" ? "episode" : "film";
  return `Puts back the original ${fmtBytes(e.src_size)}${was ? ` ${was}` : ""} file, removes the ${fmtBytes(e.out_size)}${now ? ` ${now}` : ""} file, ` +
    `and stops Convert from picking this ${what} again (converting it by hand still works).`;
}

// RevertButton puts a conversion's original back from the hold, after a confirm that says
// exactly what will happen. Give it the ledger row, or just its id (the Library) and it
// reads the row first.
export function RevertButton({ e, id, flash, onDone }: {
  e?: ConvertHistoryEntry; id?: number; flash: (m: string) => void; onDone: (e: ConvertHistoryEntry) => void;
}) {
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  if (e && (e.hold_state !== "held" || e.outcome !== "done")) return null;
  const rowID = e?.id ?? id;
  if (!rowID) return null;
  const revert = async () => {
    setBusy(true);
    try {
      const row = e ?? await convertApi.convertHistoryEntry(rowID);
      const ok = await confirm({ title: `Revert the conversion of ${row.title}?`, body: revertBody(row), confirmLabel: "Revert" });
      if (!ok) return;
      const next = await holdApi.revert(rowID);
      flash(`Reverted ${row.title} — the original is back`);
      onDone(next);
    } catch (err) { flash((err as Error).message); } finally { setBusy(false); }
  };
  return (
    <button onClick={revert} disabled={busy} title="Put the original back in place of the converted file"
      className="rounded-lg px-2.5 py-1 text-[11px] font-semibold disabled:opacity-50" style={{ border: "1px solid var(--accent-line)", color: "var(--accent)" }}>
      {busy ? "…" : "Revert"}
    </button>
  );
}

// ReleaseButton deletes one row's held original now, after a confirm.
export function ReleaseButton({ e, flash, onDone }: { e: ConvertHistoryEntry; flash: (m: string) => void; onDone: (e: ConvertHistoryEntry) => void }) {
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  if (e.hold_state !== "held" || e.outcome !== "done") return null;
  const release = async () => {
    const ok = await confirm({
      title: `Release the original of ${e.title}?`,
      body: `Deletes the original (${fmtBytes(e.src_size)}) for good, freeing about ${fmtBytes(Math.max(0, e.src_size - e.out_size))}. This conversion can't be undone after.`,
      confirmLabel: "Release",
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const next = await holdApi.release(e.id);
      flash(`Released the original of ${e.title}`);
      onDone(next);
    } catch (err) { flash((err as Error).message); } finally { setBusy(false); }
  };
  return (
    <button onClick={release} disabled={busy} className="rounded-lg px-2.5 py-1 text-[11px] font-semibold disabled:opacity-50" style={ghostBtn}>
      {busy ? "…" : "Release now"}
    </button>
  );
}
