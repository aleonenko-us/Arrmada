import { useEffect, useState } from "react";
import { FIT_COLOR } from "../../components/FitBadge";
import { type FitItem, type FitStatus, type Series as SeriesT, type SeriesRenameItem } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";
import { Button } from "../../ui";
import type { Flash } from "./HeroActions";
import { DuplicatesPanel, SeriesBlocklistPanel } from "./panels";
import { RenameModal } from "./RenameModal";
import { fmtSize, seasonName, sortSeasons } from "./util";

const FIT_WORD: Record<FitStatus, string> = { fits: "fit", over: "over", under: "under", mismatch: "don't fit" };
const base = (p: string) => p.slice(Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\")) + 1);

// FilesTab is what's on disk for the show: each season's files, size, folder and how they
// fit the profile, the rename preview with Rename…, duplicate copies and the blocklist.
export function FilesTab({ series, fits, refreshKey, downloading, onChange, flash }: {
  series: SeriesT;
  fits: Map<string, FitItem>;
  refreshKey: unknown;
  downloading: number;
  onChange: () => void;
  flash: Flash;
}) {
  const seasons = sortSeasons(series.seasons).filter((sn) => (sn.episodes ?? []).length > 0);
  return (
    <div className="flex flex-col gap-8">
      <section aria-label="Files by season">
        <h2 className="m-0 mb-3 text-md font-bold">On disk</h2>
        <div className="overflow-hidden rounded-xl" style={{ border: "1px solid var(--line)" }}>
          {seasons.map((sn) => {
            const eps = sn.episodes ?? [];
            const files = eps.filter((e) => e.has_file);
            const size = files.reduce((n, e) => n + (e.size_bytes ?? 0), 0);
            const dir = (files.find((e) => e.file_path)?.file_path ?? "").replace(/[\\/][^\\/]*$/, "");
            const counts = new Map<FitStatus, number>();
            for (const e of files) {
              const st = fits.get(`${e.season_number}:${e.episode_number}`)?.fit?.status;
              if (st) counts.set(st, (counts.get(st) ?? 0) + 1);
            }
            return (
              <div key={sn.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3.5 py-2.5 text-sm [&:not(:last-child)]:border-b" style={{ borderColor: "var(--line-soft)", background: "var(--panel)" }}>
                <span className="w-[86px] flex-none font-semibold">{seasonName(sn.season_number)}</span>
                <span className="font-mono text-xs text-ink-dim">{files.length}/{eps.length} files · {fmtSize(size)}</span>
                <span className="flex flex-wrap gap-1.5">
                  {(["fits", "over", "under", "mismatch"] as FitStatus[]).filter((k) => counts.get(k)).map((k) => (
                    <span key={k} className="rounded px-1.5 py-0.5 font-mono text-xs font-bold" style={{ color: FIT_COLOR[k], border: `1px solid ${FIT_COLOR[k]}` }}>{counts.get(k)} {FIT_WORD[k]}</span>
                  ))}
                </span>
                {dir && <span className="min-w-0 basis-full truncate font-mono text-xs text-ink-faint sm:ml-auto sm:basis-auto" title={dir}>{dir}</span>}
              </div>
            );
          })}
          {seasons.length === 0 && <div className="p-6 text-center text-base text-ink-dim">No episodes are listed yet.</div>}
        </div>
      </section>

      <RenamePreview series={series} refreshKey={refreshKey} onChange={onChange} />
      <DuplicatesPanel seriesId={series.id} refreshKey={refreshKey} flash={flash} />
      {/* A block isn't always a history event (a junk download is blocklisted as it
          leaves the queue), so the panel also reloads when a download goes away. */}
      <SeriesBlocklistPanel seriesId={series.id} refreshKey={`${String(refreshKey)}:${downloading}`} />
    </div>
  );
}

// RenamePreview shows inline which files the naming scheme would rename; Rename… opens
// the full old → new review, and nothing moves before it's confirmed there.
function RenamePreview({ series, refreshKey, onChange }: { series: SeriesT; refreshKey: unknown; onChange: () => void }) {
  const [items, setItems] = useState<SeriesRenameItem[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    let alive = true;
    seriesApi.seriesRenamePreview(series.id)
      .then((r) => { if (alive) { setItems(r.items ?? []); setErr(null); } })
      .catch((e: Error) => { if (alive) setErr(e.message); });
    return () => { alive = false; };
  }, [series.id, refreshKey]);
  const shown = (items ?? []).slice(0, 8);
  return (
    <section aria-label="Rename">
      <div className="mb-2 flex items-center gap-3">
        <h2 className="m-0 text-md font-bold">Rename</h2>
        {items && items.length > 0 && <Button size="sm" className="ml-auto" onClick={() => setOpen(true)}>Rename…</Button>}
      </div>
      {err ? (
        <p className="m-0 text-sm" style={{ color: "var(--reject)" }}>{err}</p>
      ) : items === null ? (
        <p className="m-0 text-sm text-ink-dim">Checking file names…</p>
      ) : items.length === 0 ? (
        <p className="m-0 text-sm text-ink-dim">Every file already follows the naming scheme.</p>
      ) : (
        <div className="rounded-xl p-3 text-sm" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
          <div className="mb-1.5 text-ink-dim">{items.length} file{items.length === 1 ? "" : "s"} would be renamed{items.some((it) => it.conflict) ? " (some can't be — see Rename…)" : ""}.</div>
          {shown.map((it) => (
            <div key={it.from} className="truncate py-0.5 font-mono text-xs" title={`${it.from} → ${it.to}`}>
              <span className="text-ink-faint">{base(it.from)}</span> → <span style={{ color: it.conflict ? "var(--reject)" : "var(--ink)" }}>{base(it.to)}</span>
            </div>
          ))}
          {items.length > shown.length && <div className="mt-1 text-ink-faint">…and {items.length - shown.length} more</div>}
        </div>
      )}
      {open && <RenameModal seriesId={series.id} title={series.title} onClose={() => setOpen(false)} onRenamed={onChange} />}
    </section>
  );
}
