// renderNaming mirrors the backend renderName (internal/library/importer.go) for the
// Settings → Naming previews: substitute tokens, drop empty Plex groups ("{edition-}",
// "{tmdb-}"…) and bracket pairs, stranded "- -" separators, and illegal filename chars.
export function renderNaming(format: string, sample: Record<string, string>): string {
  let out = format;
  for (const [k, v] of Object.entries(sample)) out = out.split(`{${k}}`).join(v);
  out = out.replace(/\{(edition|tmdb|imdb|tvdb)-\s*\}/g, "");
  out = out.replace(/\(\)/g, "").replace(/\[\]/g, "").replace(/\s+/g, " ");
  while (out.includes("- -")) out = out.replace("- -", "-");
  out = out.replace(/^[\s-]+|[\s-]+$/g, "");
  return out.replace(/[<>:"/\\|?*]/g, "");
}

// seriesIdFormat mirrors the backend's "{tvdb-{tvdbid}}" fallback: a show TVDB doesn't
// know gets its TMDB id tag instead.
export function seriesIdFormat(format: string, ids: { tmdbid?: string; tvdbid?: string }): string {
  if (!ids.tvdbid && ids.tmdbid) return format.split("{tvdb-{tvdbid}}").join("{tmdb-{tmdbid}}");
  return format;
}
