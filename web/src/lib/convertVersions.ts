// Convert lists every file a movie holds: its default file and each extra version (a 4K
// remux kept next to the 1080p copy, say). In the Library a version sits right under its
// film's default file, indented, so a film's files read together whatever the sort.

export interface VersionRow {
  movie_id?: number;
  version_id?: number;
}

// groupVersions keeps the sorted order of the default files (and of everything that isn't
// a movie version) and moves each version to just after its film's default file. A version
// whose default file isn't in the list (filtered out, or the film has only versions) keeps
// its own sorted place.
export function groupVersions<T extends VersionRow>(list: T[]): T[] {
  const listed = new Set<number>();
  for (const c of list) if (!c.version_id && c.movie_id) listed.add(c.movie_id);
  const under = new Map<number, T[]>();
  for (const c of list) {
    if (c.version_id && c.movie_id && listed.has(c.movie_id)) {
      const rows = under.get(c.movie_id) ?? [];
      rows.push(c);
      under.set(c.movie_id, rows);
    }
  }
  const out: T[] = [];
  for (const c of list) {
    if (c.version_id && c.movie_id && listed.has(c.movie_id)) continue;
    out.push(c);
    if (!c.version_id && c.movie_id) out.push(...(under.get(c.movie_id) ?? []));
  }
  return out;
}

// nestedVersions is the version rows shown indented under their film: those whose film's
// default file is in the list (groupVersions puts it just above them).
export function nestedVersions<T extends VersionRow>(list: T[]): Set<T> {
  const listed = new Set<number>();
  for (const c of list) if (!c.version_id && c.movie_id) listed.add(c.movie_id);
  return new Set(list.filter((c) => !!c.version_id && !!c.movie_id && listed.has(c.movie_id)));
}
