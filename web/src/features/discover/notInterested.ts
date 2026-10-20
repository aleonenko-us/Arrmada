import type { DiscoverCard } from "../../lib/api";
import type { ToastFn } from "../../ui";
import { discoverApi } from "./discoverApi";

// "Not interested": the card is already gone from the page (the caller dropped it); this
// tells the server, and the toast offers Undo. Its own chunk, loaded on the first use.
export async function hideTitle(c: DiscoverCard, flash: ToastFn, putBack: () => void): Promise<void> {
  const name = c.display_title || c.title;
  try {
    await discoverApi.hide(c);
  } catch (e) {
    putBack();
    flash(`Couldn’t hide “${name}” — ${(e as Error).message}`, { tone: "error" });
    return;
  }
  flash(`Hidden “${name}” from your Discover`, {
    ms: 7000,
    action: {
      label: "Undo",
      onClick: () => {
        putBack();
        discoverApi.restore(c.media_type, c.tmdb_id).catch(() => flash("Couldn’t undo that — restore it from Hidden titles.", { tone: "error" }));
      },
    },
  });
}
