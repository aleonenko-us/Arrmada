import type { QualityProfileInfo } from "../../lib/api";

// The staff profile picker on a Discover title (issue #50): which profile a request is
// sent with. Only the staff picker's lazy chunk imports this.

/** The profiles a request for this media can be added with (the list API is per media, but
 * the picker never offers another media's profile even if one slipped through). */
export function requestProfiles(all: QualityProfileInfo[], media: "movie" | "series"): QualityProfileInfo[] {
  return all.filter((p) => p.media_type === media);
}

/** The profile the button shows and sends: the one picked, else the default, else the
 * first. undefined when there are none (the request then goes without one, and the server
 * uses its default). A pick that's no longer listed falls back the same way. */
export function selectedProfile(profiles: QualityProfileInfo[], picked: string): QualityProfileInfo | undefined {
  return profiles.find((p) => p.key === picked) ?? profiles.find((p) => p.is_default) ?? profiles[0];
}
