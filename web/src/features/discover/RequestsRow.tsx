import { Suspense, useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type MediaRequest } from "../../lib/api";
import { lazyPage } from "../../lib/lazyPage";
import { useMe, isStaff } from "../../lib/me";
import { posterThumb } from "../../lib/img";
import { formatSeasons, MOVING_STAGES, requestStage, sortForRequester } from "../../lib/requestStage";
import { useRequestsFeed } from "../../lib/useRequestsFeed";
import { StatusChip, POSTER_CHIP_BG, TONE_HUE } from "../../ui";
import { PosterPlaceholder } from "./shared";
import { firstWatch } from "../../components/brand/servers";
import { RailArrows, RailStrip, useRail } from "./rail";

// The request sheet loads when a request is first opened, not with Discover.
const RequestSheet = lazyPage(() => import("../../components/RequestSheet"), "RequestSheet");

// MyRequestsRow is the strip of requests, first thing on Discover: a capped view of the
// Requests page. Staff see what's waiting for them first (oldest first), then what's on its
// way; everyone else their own and followed requests, what's moving first. Each poster is
// one button that opens the RequestSheet — no action happens from a tap on the strip.
export function MyRequestsRow() {
  const { user } = useMe();
  const staff = isStaff(user);
  const [items, setItems] = useState<MediaRequest[] | null>(null);
  const [waiting, setWaiting] = useState(0);

  // false while downloads can't be checked: a request's progress is then unknown.
  const [queueKnown, setQueueKnown] = useState(true);
  const load = useCallback(() => api.requests({ section: "strip" }).then((r) => {
    setItems(r.requests);
    setWaiting(r.counts?.needs_approval ?? 0);
    setQueueKnown(r.client_health?.ok ?? true);
  }).catch(() => setItems([])), []);
  // At once when a request it shows changes (the live feed), else a backstop: every 10 s
  // while something is downloading or importing, once a minute otherwise, never hidden.
  const moving = (items ?? []).filter((rq) => MOVING_STAGES.has(rq.tracking?.stage ?? "")).length;
  useEffect(() => { void load(); }, [load]);
  useRequestsFeed(load, items);

  if (!items || items.length === 0) return null;
  // Staff keep the server's order: waiting (oldest first), then in progress.
  const sorted = staff ? items : sortForRequester(items);
  const heading = (
    <>
      {staff ? "Requests" : "Your requests"}
      {staff && waiting > 0 && <span className="ml-2 text-sm font-medium" style={{ color: "var(--avoid-text)" }}>{waiting} waiting</span>}
      {moving > 0 && <span className="ml-2 text-sm font-medium" style={{ color: "var(--accent-text)" }}>{moving} on the way</span>}
    </>
  );
  return <RequestRail heading={heading} seeAll={staff && waiting > 0 ? "/requests?tab=needs" : "/requests"} items={sorted} staff={staff} queueKnown={queueKnown} onChanged={load} />;
}

// ReadyForYouRow is what the viewer asked for (or follows) that arrived in the last month,
// newest first, each with Watch on Plex when the owner's Plex has it. Staff see their own
// here too, not everyone's. Nothing renders when nothing is ready.
export function ReadyForYouRow() {
  const [items, setItems] = useState<MediaRequest[] | null>(null);
  const load = useCallback(() => api.requests({ section: "ready", ready_within_days: 30, mine: 1, limit: 20 })
    .then((r) => setItems(r.requests)).catch(() => setItems([])), []);
  useEffect(() => { void load(); }, [load]);
  useRequestsFeed(load, null); // what's ready doesn't move on its own: the slow backstop
  if (!items || items.length === 0) return null;
  return <RequestRail heading="Ready for you" seeAll="/requests?tab=ready" seeAllLabel="See all ready requests" items={items} staff={false} onChanged={load} />;
}

// RequestRail is a strip of request posters, each opening its RequestSheet.
function RequestRail({ heading, seeAll, seeAllLabel, items, staff, queueKnown = true, onChanged }: { heading: React.ReactNode; seeAll: string; seeAllLabel?: string; items: MediaRequest[]; staff: boolean; queueKnown?: boolean; onChanged: () => void }) {
  const [openID, setOpenID] = useState<number | null>(null);
  const rail = useRail();
  const open = openID ? items.find((rq) => rq.id === openID) : undefined;
  return (
    <div>
      <div className="mb-2.5 flex items-center justify-between gap-2">
        <h2 className="m-0 min-w-0 text-lg font-bold">{heading}</h2>
        <div className="flex flex-none items-center gap-2">
          <Link to={seeAll} aria-label={seeAllLabel} className="text-sm font-semibold" style={{ color: "var(--accent-text)" }}>See all →</Link>
          <RailArrows rail={rail} />
        </div>
      </div>
      <RailStrip rail={rail}>
        {items.map((rq) => <RequestPoster key={rq.id} rq={rq} staff={staff} queueKnown={queueKnown} onOpen={() => setOpenID(rq.id)} />)}
      </RailStrip>
      {openID !== null && (
        <Suspense fallback={null}>
          <RequestSheet key={openID} requestId={openID} initial={open} onChanged={onChanged} onClose={() => setOpenID(null)} />
        </Suspense>
      )}
    </div>
  );
}

function RequestPoster({ rq, staff, queueKnown = true, onOpen }: { rq: MediaRequest; staff: boolean; queueKnown?: boolean; onOpen: () => void }) {
  const tr = rq.tracking;
  const stage = requestStage(rq, queueKnown);
  const watch = firstWatch(rq);
  const pct = tr && (tr.stage === "downloading" || tr.stage === "paused") && tr.progress != null ? Math.round(tr.progress * 100) : 0;
  const settling = tr?.stage === "importing" || tr?.stage === "adding"; // on disk, nearly there: a full, pulsing bar
  const showBar = !stage.unknown && (tr?.stage === "downloading" || tr?.stage === "paused" || settling);
  const following = rq.relation === "subscriber";
  return (
    <div className="w-[150px] flex-none" style={{ scrollSnapAlign: "start" }}>
      {/* The whole poster is one button: a tap anywhere opens the sheet, where every
          action is a visible, labelled button. Nothing on the strip acts by itself. */}
      <button
        onClick={onOpen}
        aria-label={`Open the request for ${rq.display_title || rq.title}`}
        className="relative block w-full overflow-hidden rounded-xl text-left"
        style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }}
      >
        {rq.poster_url ? (
          <img src={posterThumb(rq.poster_url)} alt="" className="h-full w-full object-cover" loading="lazy" decoding="async" />
        ) : (
          <PosterPlaceholder title={rq.title} year={rq.year} />
        )}
        {/* The status badge keeps the top row to itself (with "Following" beside it): a
            150 px poster has no room for a name next to "SEARCHING". */}
        <span className="absolute inset-x-1.5 top-1.5 z-10 flex items-start justify-between gap-1">
          {following && !(staff && rq.requested_by_name) ? (
            <StatusChip tone="faint" surface="poster" className="min-w-0 truncate">Following</StatusChip>
          ) : (
            <span />
          )}
          <StatusChip tone={stage.tone} surface="poster" className="flex-none">{stage.badge}</StatusChip>
        </span>
        {/* Who asked for it — staff only, since staff see everyone's requests and "whose is
            this?" is the first question. Along the bottom, the full width of the poster, so the
            name reads whole; only a very long one truncates. Always high enough to clear the
            progress bar, so every card's chip sits at the same height. */}
        {staff && rq.requested_by_name && (
          <span
            className="absolute bottom-3 left-1.5 z-10 flex max-w-[calc(100%-12px)] items-center gap-1 rounded-full py-[2px] pl-[2px] pr-1.5"
            style={{ background: POSTER_CHIP_BG, border: "1px solid rgba(255,255,255,.18)" }}
            title={`Requested by ${rq.requested_by_name}`}
          >
            <span className="grid h-[14px] w-[14px] flex-none place-items-center rounded-full text-xs font-bold" style={{ background: "var(--accent)", color: "var(--accent-ink)" }}>
              {rq.requested_by_name[0]?.toUpperCase()}
            </span>
            <span className="truncate text-xs font-semibold text-white">{rq.requested_by_name}</span>
          </span>
        )}
        {/* Progress along the bottom while it's on its way; importing fills the bar. */}
        {showBar && (
          <span className="absolute inset-x-0 bottom-0 z-10 block h-1.5" style={{ background: "rgba(20,12,7,.55)" }}>
            <span
              className={`block h-full ${settling ? "animate-pulse" : ""}`}
              style={{ width: `${settling ? 100 : Math.max(2, pct)}%`, background: tr?.stage === "paused" ? "var(--ink-faint)" : TONE_HUE[stage.tone] }}
            />
          </span>
        )}
      </button>
      {/* Always-visible caption: the title, and where it's got to in plain words. */}
      <div className="px-0.5 pt-2">
        <div className="truncate text-sm font-semibold" style={{ color: "var(--ink)" }} title={rq.display_title || rq.title}>{rq.display_title || rq.title}</div>
        <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs" style={{ color: stage.detailTone ?? "var(--ink-faint)" }} title={stage.detail}>
          {/* A book request says what was asked for: Read, Listen or Both. */}
          {rq.media_type === "book" && rq.formats && (
            <span className="flex-none rounded px-1.5 py-px font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
              {BOOK_FORMAT_BADGE[rq.formats]}
            </span>
          )}
          {/* A show asked for by season says which: S1–3, S5. */}
          {rq.media_type === "series" && rq.seasons && rq.seasons.length > 0 && (
            <span className="flex-none font-mono text-xs font-bold" style={{ color: "var(--ink-dim)" }}>{formatSeasons(rq.seasons)}</span>
          )}
          <span className="truncate">{stage.detail}</span>
        </div>
        {/* Delivered and on a media server: one tap to watch it on the viewer's own (the
            sheet has a button per server). */}
        {watch && (
          <a href={watch.href} target="_blank" rel="noopener noreferrer" aria-label={`Watch ${rq.title} on ${watch.label}`} className="mt-1.5 inline-flex min-h-[28px] items-center rounded-md bg-accent-grad px-2.5 text-xs font-semibold text-accent-ink">▶ Watch</a>
        )}
      </div>
    </div>
  );
}

// BOOK_FORMAT_BADGE is the "what was asked for" tag on a book request. The same words as
// lib/bookFormats' FORMAT_BADGE, repeated rather than imported: a module this page and the
// Books tab (which it loads lazily) both import gets folded into this page's chunk.
const BOOK_FORMAT_BADGE = { ebook: "Read", audiobook: "Listen", both: "Both" } as const;
