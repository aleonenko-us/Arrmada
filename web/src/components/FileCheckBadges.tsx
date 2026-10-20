import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { fileCheckApi, findingBadge, type FileCheckFinding } from "../lib/fileCheckApi";
import { StatusChip } from "../ui/StatusChip";

// What the file check found in one file — "No sound", "Probably a cam" — as small badges
// beside its media chips, each naming the whole finding on hover and linking to Needs you,
// where Find a better copy and Ignore are. Nothing while the file looks right.

// A movie's or show's findings, read once per page however many files ask (each episode
// row of a show asks for the same list).
const pending = new Map<string, Promise<FileCheckFinding[]>>();

function load(kind: "movie" | "episode", ownerId: number): Promise<FileCheckFinding[]> {
  const key = `${kind}:${ownerId}`;
  let p = pending.get(key);
  if (!p) {
    p = (kind === "movie" ? fileCheckApi.forMovie(ownerId) : fileCheckApi.forSeries(ownerId))
      .then((r) => r.findings ?? [])
      .catch(() => []);
    pending.set(key, p);
    // Forget it soon, so coming back to the page reads what's true then.
    setTimeout(() => pending.delete(key), 30_000);
  }
  return p;
}

export function FileCheckBadges({ kind, ownerId, mediaId, versionId = 0, size = "sm" }: {
  kind: "movie" | "episode";
  /** The movie, or the episode's show. */
  ownerId: number;
  /** The episode (for an episode file); the movie's own id is ownerId. */
  mediaId?: number;
  versionId?: number;
  size?: "xs" | "sm";
}) {
  const [found, setFound] = useState<FileCheckFinding[]>([]);
  useEffect(() => {
    let live = true;
    void load(kind, ownerId).then((all) => {
      if (!live) return;
      setFound(all.filter((f) => (kind === "movie" ? (f.version_id ?? 0) === versionId : f.media_id === mediaId)));
    });
    return () => { live = false; };
  }, [kind, ownerId, mediaId, versionId]);
  if (found.length === 0) return null;
  return (
    <>
      {found.map((f) => (
        <Link key={f.id} to="/activity/needs-you#file-checks" className="no-underline" title={f.text}>
          <StatusChip tone={f.code === "wrong_language" ? "avoid" : "reject"} size={size}>
            {findingBadge(f.code)}{f.state === "replacing" ? " · replacing" : ""}
          </StatusChip>
        </Link>
      ))}
    </>
  );
}
