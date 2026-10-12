import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { keepSubsLine } from "../../lib/convertKeepSubs";
import { langName, useLanguages } from "../../lib/languages";
import { LINKS } from "../../lib/links";
import { posterThumb } from "../../lib/img";

// What the Subtitles page and its settings (Settings → Subtitles & Convert) share.

export const card = "rounded-xl p-4";
export const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;

// KeptTracksNote says where embedded tracks stand. Subtitles never edits the video; which
// embedded subtitles survive is Convert's own setting (which can follow this list), applied
// when it rewrites a file — so say what that setting actually is.
export function KeptTracksNote() {
  const [keep, setKeep] = useState<string | null>(null);
  const languages = useLanguages();
  useEffect(() => { api.convertSettings().then((s) => setKeep(s.keep_sub_langs ?? "")).catch(() => {}); }, []);
  return (
    <>
      Embedded subtitle tracks stay inside the video.{" "}
      {keep !== null && <>When Convert rewrites a file, it keeps embedded subtitles in {keepSubsLine(keep, (c) => langName(c, languages))}. </>}
      Set that in <Link to={LINKS.convertSettings} style={{ color: "var(--accent)" }}>Convert's settings</Link>.
    </>
  );
}

// tinyPoster is the smallest size of the same poster the library grids show: a 32px row
// thumbnail doesn't need the grid's w342.
function tinyPoster(url: string): string | undefined {
  const tmdb = url.replace(/\/t\/p\/(?:w\d+|original)\//, "/t/p/w92/");
  return tmdb !== url ? tmdb : posterThumb(url);
}

// RowPoster is a Library row's 32×48 poster, loaded only as it scrolls into view, or a
// blank tile of the same size.
export function RowPoster({ url }: { url?: string }) {
  return (
    <span className="inline-block h-12 w-8 flex-none overflow-hidden rounded" style={{ background: "var(--panel-2)", border: "1px solid var(--line-soft)" }} aria-hidden="true">
      {url && <img src={tinyPoster(url)} alt="" width={32} height={48} className="h-full w-full object-cover" loading="lazy" decoding="async" />}
    </span>
  );
}
