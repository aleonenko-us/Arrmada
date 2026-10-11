import { useEffect, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { api, type AttentionItem } from "../lib/api";
import { formatAgo } from "../lib/format";
import { LINKS, fixLink } from "../lib/links";
import { loadAttention, refreshAttention, useAttention } from "../lib/useAttention";
import { jobFailed, jobToast, useJob } from "../lib/useJob";
import { Skeleton, useToast } from "../ui";
import { CardActions, type CardAction } from "./activity/transfer";
import { ReviewsPanel } from "./Reviews";

// Activity → Needs you: every stuck download in one place, each with the action that fits
// it — decide a held import, retry a failing one, try another release for a stalled or
// errored download, move a misfiled TV download. The list is the shared Needs-you
// snapshot (lib/useAttention), already kept live over the socket for the sidebar badges,
// so this tab adds no polling of its own. Each section has an id the Dashboard card and
// health Fix links point at (/activity/needs-you#stalled).

const STALLED_KINDS = new Set(["stalled", "download"]);
// What the other sections of the feed are, for the "Elsewhere" footer.
const HERE = new Set(["review", "import", "stalled", "download", "wrongcat"]);

export function NeedsYouPanel() {
  const att = useAttention();
  const { hash } = useLocation();
  // The shell keeps the snapshot fresh; opening the tab asks once more so a link from an
  // alert lands on what is true now.
  useEffect(() => { void loadAttention(); }, []);
  // A Fix link names its section (#stalled): bring it into view once the list is there.
  const ready = !!att;
  useEffect(() => {
    if (!ready || !hash) return;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [ready, hash]);

  if (!att) return <Skeleton variant="list" count={4} />;
  const items = att.items;
  const imports = items.filter((i) => i.kind === "import");
  const stalled = items.filter((i) => STALLED_KINDS.has(i.kind));
  const wrong = items.filter((i) => i.kind === "wrongcat");
  const elsewhere = att.groups.filter((g) => !HERE.has(g.kind));

  return (
    <div className="max-w-[960px]">
      <Section id="reviews" title="Held for review" count={att.counts.reviews} hint="Finished downloads Arrmada held back instead of importing. Each says why, with only the actions that can work for it.">
        <ReviewsPanel compact />
      </Section>
      <Section id="import-failures" title="Imports failing" count={imports.length} hint="Finished downloads whose import keeps failing — usually a folder Arrmada can't write to, or a full disk. Fix the cause, then retry.">
        {imports.length === 0 ? <Empty>No import is failing.</Empty> : imports.map((it) => <ImportFailureCard key={it.key} it={it} />)}
      </Section>
      <Section id="stalled" title="Stalled or errored" count={stalled.length} hint="Downloads nobody has sent data for an hour, or that the client stopped with an error.">
        {stalled.length === 0 ? <Empty>Every download is moving.</Empty> : stalled.map((it) => <StalledCard key={it.key} it={it} />)}
      </Section>
      <Section id="wrong-category" title="Wrong category" count={wrong.length} hint="Finished TV downloads filed where the series import never looks. Move them to TV and the next import check takes them.">
        {wrong.length === 0 ? <Empty>Nothing is filed in the wrong place.</Empty> : wrong.map((it) => <WrongCategoryCard key={it.key} it={it} />)}
      </Section>
      {elsewhere.length > 0 && (
        <section aria-label="Also waiting" className="mt-2 rounded-xl px-4 py-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-ink-faint">Also waiting</div>
          <ul className="m-0 list-none p-0">
            {elsewhere.map((g) => {
              const to = fixLink(g);
              return (
                <li key={g.kind} className="flex items-center gap-2 py-0.5 text-[12.5px]">
                  <span className="min-w-0 flex-1">{g.title}</span>
                  {to && <Link to={to} className="flex-none font-semibold" style={{ color: "var(--accent)" }}>Open →</Link>}
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </div>
  );
}

function Section({ id, title, count, hint, children }: { id: string; title: string; count: number; hint: string; children: ReactNode }) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className="mb-7 scroll-mt-4">
      <h2 id={`${id}-title`} className="m-0 flex items-baseline gap-2 text-[14px] font-bold">
        {title} <span className="font-mono text-[11px] font-normal text-ink-faint">{count}</span>
      </h2>
      <p className="mb-3 mt-0.5 max-w-[72ch] text-[11.5px] text-ink-faint">{hint}</p>
      <div className="flex flex-col gap-2">{children}</div>
    </section>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-xl p-5 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>{children}</div>;
}

// ProblemCard is one stuck download: what's wrong, since when, and its actions.
function ProblemCard({ it, primary, secondary, extras }: { it: AttentionItem; primary?: ReactNode; secondary?: CardAction[]; extras?: CardAction[] }) {
  return (
    <div className="rounded-xl p-3.5" data-testid="needs-you-item" style={{ background: "var(--panel)", border: `1px solid ${it.level === "error" ? "var(--reject)" : "var(--line)"}` }}>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="break-words text-[12.5px] font-semibold [overflow-wrap:anywhere]">{it.title}</div>
          {it.detail && <div className="mt-0.5 text-[11.5px] text-ink-dim [overflow-wrap:anywhere]">{it.detail}</div>}
          {it.since > 0 && <div className="mt-0.5 font-mono text-[10px] text-ink-faint" title={new Date(it.since).toLocaleString()}>since {formatAgo(it.since)}</div>}
        </div>
        <CardActions name={it.title} primary={primary} secondary={secondary} extras={(extras ?? []).map((a) => ({ label: a.label, onSelect: a.onSelect, disabled: a.disabled, tone: a.danger ? "danger" : "default" }))} />
      </div>
    </div>
  );
}

// useAct runs one card's action: busy while it runs, a toast with how it went, and the
// Needs-you snapshot asked again so the card leaves once the server agrees.
function useAct() {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    try {
      await fn();
      toast(done, { ms: 5000 });
      refreshAttention();
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 7000 });
    } finally {
      setBusy(false);
    }
  };
  return { busy, run };
}

function PrimaryBtn({ label, onClick, disabled, title }: { label: string; onClick: () => void; disabled?: boolean; title?: string }) {
  return (
    <button onClick={onClick} disabled={disabled} title={title ?? label} className="rounded-lg px-2.5 py-1.5 text-[11px] font-semibold" style={{ border: "1px solid var(--accent)", color: "var(--accent)", opacity: disabled ? 0.5 : 1 }}>
      {label}
    </button>
  );
}

function useShowInQueue(view: "downloading" | "finishing"): CardAction {
  const navigate = useNavigate();
  return { label: "Show in queue", onSelect: () => navigate(view === "finishing" ? `${LINKS.activityQueue}?show=finishing` : LINKS.activityQueue) };
}

function ImportFailureCard({ it }: { it: AttentionItem }) {
  const { busy, run } = useAct();
  const show = useShowInQueue("finishing");
  const hash = it.hash;
  return (
    <ProblemCard
      it={it}
      primary={hash ? <PrimaryBtn label={busy ? "Retrying…" : "Retry now"} disabled={busy} title="Fix the cause first — then the next import check tries it again at once" onClick={() => void run(() => api.retryImport(hash), "Retrying — the next import check tries it again.")} /> : undefined}
      secondary={[show]}
    />
  );
}

function StalledCard({ it }: { it: AttentionItem }) {
  const { busy, run } = useAct();
  const toast = useToast();
  const show = useShowInQueue("downloading");
  const [job, setJob] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const failover = useJob(job, {
    onDone: (j) => {
      setJob(null);
      toast(jobToast(j), { tone: jobFailed(j) ? "error" : undefined, ms: 8000 });
      refreshAttention();
    },
  });
  const hash = it.hash;
  if (!hash) return <ProblemCard it={it} secondary={[show]} />;
  const searching = starting || failover.running;
  const tryAnother = async () => {
    setStarting(true);
    try {
      const r = await api.failOverDownload(hash);
      if (r.job_id) setJob(r.job_id);
      else toast("Looking for another release…", { ms: 5000 });
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 7000 });
    } finally {
      setStarting(false);
    }
  };
  const reannounce: CardAction = { label: "Reannounce", disabled: busy, title: "Ask the trackers for peers again", onSelect: () => run(() => api.torrentAction(hash, "reannounce"), "Asked the trackers for peers again.") };
  const recheck: CardAction = { label: "Recheck files", disabled: busy, title: "Check the downloaded data against the torrent", onSelect: () => run(() => api.torrentAction(hash, "recheck"), "Rechecking the downloaded data.") };
  const errored = it.kind === "download";
  // Another release is only for what Arrmada grabbed; anything else gets the client's own
  // remedies first.
  const primary = it.grabbed
    ? <PrimaryBtn label={searching ? "Searching…" : "Try another release"} disabled={searching} title="Search for a different release; this one is removed only once another is grabbed" onClick={() => void tryAnother()} />
    : <PrimaryBtn label={errored ? "Recheck files" : "Reannounce"} disabled={busy} onClick={() => void (errored ? recheck : reannounce).onSelect()} />;
  const rest = it.grabbed ? [reannounce, recheck] : [errored ? reannounce : recheck];
  return <ProblemCard it={it} primary={primary} secondary={rest} extras={[show]} />;
}

function WrongCategoryCard({ it }: { it: AttentionItem }) {
  const { busy, run } = useAct();
  const show = useShowInQueue("finishing");
  const hash = it.hash;
  // The TV category is the server's; ask for it when the button is used.
  const move = () => run(async () => {
    const { categories } = await api.downloadClientList();
    if (!categories?.tv) throw new Error("Couldn't read Arrmada's TV category — try again in a moment.");
    await api.setDownloadCategory(hash!, categories.tv);
  }, "Moved to TV — the next import check picks it up.");
  return (
    <ProblemCard
      it={it}
      primary={hash ? <PrimaryBtn label={busy ? "Moving…" : "Move to TV"} disabled={busy} title="File it under Arrmada's TV category, where the series import looks" onClick={() => void move()} /> : undefined}
      secondary={[show]}
    />
  );
}
