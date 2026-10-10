// The Activity page (/activity/<tab>): what Arrmada is downloading, what needs a person,
// what it's still looking for, what it has done, and what it won't grab again — one page
// that used to be four (Downloads, Review, History, Blocklist). The address is the
// state: the tab is the path, a tab's own sub-view lives in ?show=, so Back and deep
// links work. API paths never say "activity" (ad-block lists block it); only this page does.

export const ACTIVITY_TABS = ["queue", "needs-you", "wanted", "history", "blocklist"] as const;
export type ActivityTab = (typeof ACTIVITY_TABS)[number];

export const ACTIVITY_TAB_LABEL: Record<ActivityTab, string> = {
  queue: "Queue",
  "needs-you": "Needs you",
  wanted: "Wanted",
  history: "History",
  blocklist: "Blocklist",
};

export function isActivityTab(t: string | undefined): t is ActivityTab {
  return t !== undefined && (ACTIVITY_TABS as readonly string[]).includes(t);
}

// The pages the Activity page replaced, and the bare /activity.
export type LegacyActivityPage = "downloads" | "review" | "history" | "blocklist" | "activity";

// legacyActivityPath is where an old address lands now, keeping its query and hash so
// bookmarks, health Fix links from an older server, and notification links still open
// the same view. Downloads had its lists in ?tab= and its problems filter in ?show=; both
// are translated, every other parameter is kept as it was.
export function legacyActivityPath(from: LegacyActivityPage, search: string, hash = ""): string {
  const params = new URLSearchParams(search);
  let tab: ActivityTab;
  switch (from) {
    case "review":
      tab = "needs-you";
      break;
    case "history":
      tab = "history";
      break;
    case "blocklist":
      tab = "blocklist";
      break;
    case "activity":
      tab = "queue";
      break;
    case "downloads": {
      const old = params.get("tab");
      params.delete("tab");
      const problems = params.get("show") === "problems";
      params.delete("show");
      if (old === "searching" || old === "upcoming") {
        tab = "wanted";
        if (old === "upcoming") params.set("show", "upcoming");
      } else {
        tab = "queue";
        if (old === "seeding") params.set("show", "seeding");
        if (problems) params.set("filter", "problems");
      }
      break;
    }
  }
  const q = params.toString();
  return `/activity/${tab}${q ? `?${q}` : ""}${hash}`;
}
