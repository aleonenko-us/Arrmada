import { Link } from "react-router-dom";
import type { CastMember } from "../lib/api";
import type { OverlayState } from "../features/discover/shared";
import { personPath } from "../lib/refLink";

// Opened from a library page, a person's page comes up as Discover's overlay, one entry
// above this page: Close and Back step back here instead of landing on Discover.
const FROM_LIBRARY: OverlayState = { depth: 1, cold: false };

// CastRow is a movie's or show's billed cast on its library page. Each actor with a TMDB
// person id links to their page on Discover (everything else they've made); one from a
// record stored before ids were kept stays a plain tile until the next metadata refresh.
export function CastRow({ cast }: { cast?: CastMember[] }) {
  if (!cast || cast.length === 0) return null;
  return (
    <div className="mt-8">
      <h2 className="m-0 mb-3 text-md font-bold">Cast</h2>
      {/* The scroller clips what overflows it: a little room (taken back by the negative
          margin, so nothing moves) keeps a focused tile's outline visible. */}
      <div className="thin-scroll -mx-1 -mt-1 flex gap-3 overflow-x-auto px-1 pb-2 pt-1">
        {cast.map((c, i) => {
          const body = (
            <>
              <div className="mb-1.5 overflow-hidden rounded-lg" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }}>
                {c.profile_url ? <img src={c.profile_url} alt={c.id ? "" : c.name} className="h-full w-full object-cover transition-opacity group-hover:opacity-80" loading="lazy" /> : null}
              </div>
              <div className="truncate text-xs font-semibold group-hover:underline group-focus-visible:underline" title={c.name}>{c.name}</div>
              {c.character && <div className="truncate text-xs text-ink-faint" title={c.character}>{c.character}</div>}
            </>
          );
          const cls = "w-[92px] flex-none text-center";
          return c.id ? (
            <Link key={i} to={personPath(c.id)} state={FROM_LIBRARY} aria-label={`See ${c.name}’s movies and shows`} className={`group block rounded-lg ${cls}`}>
              {body}
            </Link>
          ) : (
            <div key={i} className={cls}>{body}</div>
          );
        })}
      </div>
    </div>
  );
}
