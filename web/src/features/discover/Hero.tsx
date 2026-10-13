import { useEffect, useState } from "react";
import { api, type DiscoverCard } from "../../lib/api";
import { usePoll } from "../../lib/usePoll";
import { StatusChip } from "../../ui";
import { badgeFor, useOpenTitle, type RowCtx } from "./shared";

// ---- Cinematic hero ---------------------------------------------------------

export function Hero({ ctx }: { ctx: RowCtx }) {
  const [items, setItems] = useState<DiscoverCard[] | null>(null);
  const [idx, setIdx] = useState(0);
  const [paused, setPaused] = useState(false);
  const [quickBusy, setQuickBusy] = useState(false);
  const openTitle = useOpenTitle();

  useEffect(() => {
    let alive = true;
    api.discoverTrending("all")
      .then((r) => { if (alive) setItems(r.filter((x) => !!x.backdrop_url).slice(0, 5)); })
      .catch(() => { if (alive) setItems([]); });
    return () => { alive = false; };
  }, []);

  const count = items?.length ?? 0;
  usePoll(() => setIdx((i) => (i + 1) % count), paused || count <= 1 ? null : 7000, { immediate: false });
  useEffect(() => { if (count > 0 && idx >= count) setIdx(0); }, [idx, count]);

  if (items === null) return <div className="w-full animate-pulse rounded-2xl" style={{ height: "clamp(340px, 46vh, 560px)", background: "var(--panel-2)", border: "1px solid var(--line)" }} />;
  if (count === 0) return null;

  const cur = items[Math.min(idx, count - 1)];
  const requested = ctx.isRequested(cur);
  const badge = badgeFor(cur, requested);
  const requestable = !badge;

  const quick = async () => {
    // A show is never asked for whole in one click: the sheet asks which seasons.
    if (cur.media_type === "series") { openTitle(cur, true); return; }
    if (quickBusy) return;
    setQuickBusy(true);
    try { await ctx.doRequest(cur); } catch { /* toast shown */ } finally { setQuickBusy(false); }
  };

  return (
    <div
      className="relative overflow-hidden rounded-2xl"
      style={{ height: "clamp(340px, 46vh, 560px)", border: "1px solid var(--line)" }}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      {/* Stacked backdrops crossfade; rendering all 5 also preloads them. */}
      {items.map((it, i) => (
        <img
          key={it.tmdb_id}
          src={it.backdrop_url}
          alt=""
          aria-hidden
          className="absolute inset-0 h-full w-full object-cover transition-opacity duration-700"
          style={{ opacity: i === idx ? 1 : 0 }}
          loading={i === 0 ? "eager" : "lazy"}
          decoding="async"
        />
      ))}
      {/* Scrims: left→right for text legibility, bottom melts into the page. */}
      <div className="absolute inset-0" style={{ background: "linear-gradient(to right, rgba(0,0,0,.85) 0%, rgba(0,0,0,.55) 38%, rgba(0,0,0,0) 72%)" }} />
      <div className="absolute inset-x-0 bottom-0 h-2/3" style={{ background: "linear-gradient(to top, var(--bg) 2%, transparent)" }} />

      <div className="relative z-10 flex h-full max-w-[640px] flex-col justify-end gap-2.5 p-5 sm:p-8">
        {badge && (
          <StatusChip tone={badge.tone} surface="poster" size="md" className="w-fit">{badge.label}</StatusChip>
        )}
        <h2 className="m-0 line-clamp-2 text-3xl font-extrabold leading-[1.08] sm:text-display-lg" style={{ color: "#fff", textShadow: "0 2px 18px rgba(0,0,0,.55)" }}>{cur.title}</h2>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm font-medium" style={{ color: "rgba(255,255,255,.82)" }}>
          {cur.year ? <span>{cur.year}</span> : null}
          {cur.vote_average > 0 && <span style={{ color: "var(--accent)" }}>★ {cur.vote_average.toFixed(1)}</span>}
          <span className="rounded px-1.5 py-px text-xs uppercase" style={{ background: "rgba(255,255,255,.14)" }}>{cur.media_type === "series" ? "TV" : "Movie"}</span>
        </div>
        {cur.overview && <p className="m-0 line-clamp-2 max-w-[560px] text-md leading-relaxed sm:line-clamp-3" style={{ color: "rgba(255,255,255,.78)" }}>{cur.overview}</p>}
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <button
            onClick={() => openTitle(cur)}
            className="rounded-lg px-4 py-2 text-base font-semibold backdrop-blur-sm"
            style={{ background: "rgba(255,255,255,.16)", border: "1px solid rgba(255,255,255,.28)", color: "#fff" }}
          >
            View details
          </button>
          {ctx.canRequest && requestable && (
            <button
              onClick={quick}
              disabled={quickBusy}
              className="rounded-lg px-4 py-2 text-base font-semibold"
              style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: quickBusy ? 0.65 : 1 }}
            >
              {quickBusy ? "Requesting…" : "＋ Request"}
            </button>
          )}
        </div>
      </div>

      {count > 1 && (
        <div className="absolute bottom-4 right-5 z-10 flex gap-1.5">
          {items.map((it, i) => (
            <button
              key={it.tmdb_id}
              onClick={() => setIdx(i)}
              aria-label={`Show slide ${i + 1}`}
              className="h-1.5 rounded-full transition-all"
              style={{ width: i === idx ? 20 : 6, background: i === idx ? "#fff" : "rgba(255,255,255,.45)" }}
            />
          ))}
        </div>
      )}
    </div>
  );
}
