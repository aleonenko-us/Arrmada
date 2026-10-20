import { useState } from "react";

// SiteIcon is a tracker's or indexer's own icon, fetched once by the server and kept there
// (only sites on its list: internal/siteicon), on a small tile. Until it loads, and for a site
// with no icon, the tile shows the monogram instead.

// Names (lowercased, letters and digits only) that mean a known site's icon. A name
// containing one of these gets it: "NZBgeek (Prowlarr)" still finds nzbgeek.
const BY_NAME: [string, string][] = [
  ["torrentleech", "torrentleech"], ["myanonamouse", "myanonamouse"], ["mam", "myanonamouse"],
  ["1337x", "1337x"], ["prowlarr", "prowlarr"], ["iptorrents", "iptorrents"],
  ["torrentday", "torrentday"], ["yts", "yts"], ["eztv", "eztv"], ["nyaa", "nyaa"],
  ["piratebay", "thepiratebay"], ["tpb", "thepiratebay"], ["limetorrents", "limetorrents"],
  ["torrentgalaxy", "torrentgalaxy"], ["filelist", "filelist"], ["beyondhd", "beyondhd"],
  ["passthepopcorn", "passthepopcorn"], ["broadcasthenet", "broadcasthenet"],
  ["nzbgeek", "nzbgeek"], ["drunkenslug", "drunkenslug"], ["nzbfinder", "nzbfinder"],
  ["nzbplanet", "nzbplanet"], ["dognzb", "dognzb"], ["althub", "althub"],
  ["ninjacentral", "ninjacentral"], ["nzbsu", "nzbsu"], ["tabularasa", "tabularasa"],
];

export function siteIconFor(name: string): string | undefined {
  const n = name.toLowerCase().replace(/[^a-z0-9]/g, "");
  if (!n) return undefined;
  return BY_NAME.find(([k]) => (k.length <= 3 ? n === k : n.includes(k)))?.[1];
}

export const monogramOf = (name: string) => (name.replace(/[^A-Za-z0-9]/g, "").slice(0, 2) || "?").toUpperCase();

export function SiteIcon({ slug, monogram, size = 36 }: { slug?: string; monogram: string; size?: number }) {
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const showImg = !!slug && !failed;
  return (
    <span
      aria-hidden
      className="relative grid flex-none place-items-center overflow-hidden rounded-lg font-mono font-bold"
      style={{
        width: size, height: size, fontSize: Math.round(size * 0.36),
        background: showImg && loaded ? "#fff" : "var(--accent-soft)",
        color: "var(--accent-text)",
        boxShadow: "inset 0 0 0 1px var(--line)",
      }}
    >
      {(!showImg || !loaded) && monogram}
      {showImg && (
        <img
          src={`/api/v1/site-icons/${slug}`}
          alt=""
          loading="lazy"
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          className="absolute"
          style={{ width: size * 0.62, height: size * 0.62, objectFit: "contain", opacity: loaded ? 1 : 0 }}
        />
      )}
    </span>
  );
}
