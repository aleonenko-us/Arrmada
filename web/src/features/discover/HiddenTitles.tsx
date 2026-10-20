import { useEffect, useState } from "react";
import { posterThumb } from "../../lib/img";
import { Button, Sheet } from "../../ui";
import { discoverApi, type HiddenTitle } from "./discoverApi";
import type { RowCtx } from "./shared";

// HiddenTitles is the foot of Discover: "Hidden titles (N)", the titles this person said
// they're not interested in, each with Restore. Nothing shows until something is hidden.
// Its own chunk; it reads the list again whenever something is hidden on the page.

type Kind = "all" | "movie" | "series";

export function HiddenTitles({ ctx }: { ctx: RowCtx }) {
  const [items, setItems] = useState<HiddenTitle[] | null>(null);
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<Kind>("all");
  const [busy, setBusy] = useState<string | null>(null);
  const hiddenNow = ctx.hidden?.size ?? 0;

  useEffect(() => {
    let alive = true;
    // A moment after a hide, so the server has it.
    const t = setTimeout(() => {
      discoverApi.hidden().then((l) => { if (alive) setItems(l); }).catch(() => { if (alive) setItems((cur) => cur ?? []); });
    }, hiddenNow ? 600 : 0);
    return () => { alive = false; clearTimeout(t); };
  }, [hiddenNow]);

  if (!items || items.length === 0) return null;
  const both = items.some((i) => i.media_type === "movie") && items.some((i) => i.media_type === "series");
  const shown = both && kind !== "all" ? items.filter((i) => i.media_type === kind) : items;

  const restore = (h: HiddenTitle) => {
    const key = `${h.media_type}:${h.tmdb_id}`;
    setBusy(key);
    discoverApi.restore(h.media_type, h.tmdb_id)
      .then(() => {
        setItems((l) => (l ?? []).filter((i) => `${i.media_type}:${i.tmdb_id}` !== key));
        ctx.hide?.(h, true);
        ctx.flash(`“${h.title}” is back on your Discover`);
      })
      .catch((e) => ctx.flash((e as Error).message, { tone: "error" }))
      .finally(() => setBusy(null));
  };

  return (
    <div className="flex justify-center">
      <button onClick={() => setOpen(true)} className="min-h-[32px] text-sm font-semibold text-ink-faint hover:text-[var(--ink)]">
        Hidden titles ({items.length})
      </button>
      {open && (
        <Sheet onClose={() => setOpen(false)} labelledBy="hidden-titles-heading" size="md">
          <div className="px-4 pb-5 pt-3 sm:px-5 sm:pt-5">
            <h2 id="hidden-titles-heading" className="m-0 mb-1 text-lg font-bold">Hidden titles</h2>
            <p className="m-0 mb-3 text-sm text-ink-dim">Titles you said you’re not interested in. They stay off your Discover until you restore them.</p>
            {both && (
              <div className="mb-3 flex gap-1.5" role="group" aria-label="Show">
                {(["all", "movie", "series"] as const).map((k) => (
                  <button key={k} onClick={() => setKind(k)} aria-pressed={kind === k} className="min-h-[30px] rounded-full px-3 text-sm font-semibold" style={{ border: `1px solid ${kind === k ? "var(--accent)" : "var(--line)"}`, background: kind === k ? "var(--accent-soft)" : "var(--panel)", color: kind === k ? "var(--accent-text)" : "var(--ink-faint)" }}>
                    {k === "all" ? "All" : k === "movie" ? "Movies" : "Series"}
                  </button>
                ))}
              </div>
            )}
            <ul className="m-0 flex list-none flex-col gap-2 p-0">
              {shown.map((h) => {
                const key = `${h.media_type}:${h.tmdb_id}`;
                return (
                  <li key={key} className="flex items-center gap-3 rounded-lg p-2" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
                    <span className="h-[60px] w-[40px] flex-none overflow-hidden rounded" style={{ background: "var(--panel)" }}>
                      {h.poster_url ? <img src={posterThumb(h.poster_url)} alt="" className="h-full w-full object-cover" loading="lazy" /> : null}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-base font-semibold">{h.title || "Untitled"}</span>
                      <span className="block text-xs text-ink-faint">{h.media_type === "series" ? "Series" : "Movie"}{h.year ? ` · ${h.year}` : ""}</span>
                    </span>
                    <Button size="sm" onClick={() => restore(h)} busy={busy === key} busyLabel="Restoring…" aria-label={`Restore ${h.title}`}>Restore</Button>
                  </li>
                );
              })}
            </ul>
          </div>
        </Sheet>
      )}
    </div>
  );
}
