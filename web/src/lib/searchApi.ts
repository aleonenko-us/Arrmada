import { req } from "./api";
import type { LibraryHit } from "./palette";

// The command palette's library lookup (GET /api/v1/library/search, staff only). Its own
// module so it loads with the palette's chunk, not with anyone's first load.
export const searchApi = {
  library: (q: string) => req<{ results: LibraryHit[] }>(`/api/v1/library/search?q=${encodeURIComponent(q)}`).then((r) => r.results ?? []),
};
