import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { convertApi } from "../../lib/convertApi";
import { keepSubsLine } from "../../lib/convertKeepSubs";
import { langName, useLanguages } from "../../lib/languages";
import { LINKS } from "../../lib/links";

// What the Subtitles page and its settings (Settings → Subtitles & Convert) share.

export const card = "rounded-xl p-4";
export const cardStyle = { border: "1px solid var(--line)", background: "var(--panel)" } as const;

// KeptTracksNote says where embedded tracks stand. Subtitles never edits the video; which
// embedded subtitles survive is Convert's own setting (which can follow this list), applied
// when it rewrites a file — so say what that setting actually is.
export function KeptTracksNote() {
  const [keep, setKeep] = useState<string | null>(null);
  const languages = useLanguages();
  useEffect(() => { convertApi.convertSettings().then((s) => setKeep(s.keep_sub_langs ?? "")).catch(() => {}); }, []);
  return (
    <>
      Embedded subtitle tracks stay inside the video.{" "}
      {keep !== null && <>When Convert rewrites a file, it keeps embedded subtitles in {keepSubsLine(keep, (c) => langName(c, languages))}. </>}
      Set that in <Link to={LINKS.convertSettings} style={{ color: "var(--accent)" }}>Convert's settings</Link>.
    </>
  );
}
