import { Suspense } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { ACTIVITY_TABS, ACTIVITY_TAB_LABEL, isActivityTab, type ActivityTab } from "../lib/activity";
import { lazyPage } from "../lib/lazyPage";
import { useAttention } from "../lib/useAttention";
import { Skeleton } from "../ui";
import { TabPanel, Tabs, type TabItem } from "../ui/Tabs";

// Each tab is its own lazy component and only the visible one is mounted, so only it
// polls: leaving Queue stops the three-second queue read. The panels came from the pages
// this one replaced, and can be rebuilt one at a time without touching the others.
const QueuePanel = lazyPage(() => import("./Downloads"), "QueuePanel");
const WantedPanel = lazyPage(() => import("./Downloads"), "WantedPanel");
const ReviewsPanel = lazyPage(() => import("./Reviews"), "ReviewsPanel");
const HistoryPanel = lazyPage(() => import("./History"), "HistoryPanel");
const BlocklistPanel = lazyPage(() => import("./Blocklist"), "BlocklistPanel");

function panelFor(tab: ActivityTab) {
  switch (tab) {
    case "queue": return <QueuePanel />;
    case "needs-you": return <ReviewsPanel />;
    case "wanted": return <WantedPanel />;
    case "history": return <HistoryPanel />;
    case "blocklist": return <BlocklistPanel />;
  }
}

// Activity — what Arrmada is downloading, what needs you, what it's still looking for,
// what it has done, and what it won't grab again. The tab is the address (/activity/wanted),
// so Back steps through tabs and every link can open the right one.
export function Activity() {
  const { tab } = useParams();
  const navigate = useNavigate();
  // The shell already polls the Needs-you feed; its held-import count labels the tab.
  const held = useAttention()?.counts.reviews ?? 0;
  if (!isActivityTab(tab)) return <Navigate to="/activity/queue" replace />;

  const tabs: TabItem<ActivityTab>[] = ACTIVITY_TABS.map((key) => ({
    key,
    label: ACTIVITY_TAB_LABEL[key],
    count: key === "needs-you" && held > 0 ? held : undefined,
  }));

  return (
    <>
      <PageHeader title="Activity" crumb={`Activity / ${ACTIVITY_TAB_LABEL[tab]}`} />
      <div className="mx-auto w-full max-w-[1360px] px-4 py-6 sm:px-6">
        {/* A tab's filters are its own: switching tabs starts the next one clean. */}
        <Tabs tabs={tabs} value={tab} onChange={(t) => navigate(`/activity/${t}`)} idPrefix="activity" label="Activity" className="mb-4 border-b" />
        <TabPanel idPrefix="activity" value={tab}>
          <Suspense fallback={<Skeleton variant="list" count={4} />}>
            <div key={tab}>{panelFor(tab)}</div>
          </Suspense>
        </TabPanel>
      </div>
    </>
  );
}
