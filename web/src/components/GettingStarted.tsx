import { useState } from "react";
import { Link } from "react-router-dom";
import { fixLink } from "../lib/links";
import { useQuery } from "../lib/query";
import { usePoll } from "../lib/usePoll";
import { connectionsApi, type ConnectionKind } from "../lib/connectionsApi";
import { setupApi, type ChecklistItem, type SetupChecklist } from "../lib/setupApi";
import { Button, useToast } from "../ui";
import { LibraryImport } from "./LibraryImport";
import { Icon, type IconName } from "./icons";

// Getting started: the steps from a fresh install to the first imported download, each
// ticked by the server from what's really set up. The Dashboard shows it (admins only)
// until every step is done or it's dismissed; the setup wizard's last step shows the same
// rows.

const CHECKLIST_KEY = "setup:checklist";
const RECHECK_MS = 30_000;

// useSetupChecklist reads the list and keeps it current while it's on screen: every 30 s
// while the tab is visible, and straight away when the tab comes back into view. Once
// everything is done (or put away, when the card is all that uses it) it stops asking.
export function useSetupChecklist(enabled = true, { stopWhenDismissed = false } = {}) {
  const q = useQuery<SetupChecklist>(CHECKLIST_KEY, setupApi.checklist, { enabled, staleMs: 5_000 });
  const settled = !!q.data && (q.data.done || (stopWhenDismissed && q.data.dismissed));
  usePoll(q.refetch, enabled && !settled ? RECHECK_MS : null, { immediate: false });
  return q;
}

// Each step's picture on its tile.
const STEP_ICONS: Record<string, IconName> = {
  tmdb: "discover", folders: "movies", download_client: "downloadClients", indexer: "indexers",
  media_server: "insights", first_title: "requests", first_import: "history", import_library: "downloads",
};

// StepTile is a step's icon, or a check once it's done.
function StepTile({ item, next }: { item: ChecklistItem; next?: boolean }) {
  if (item.done) {
    return (
      <span aria-hidden className="grid h-8 w-8 flex-none place-items-center rounded-full" style={{ background: "var(--good-soft)", color: "var(--good-text)" }}>
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M3.5 8.5l3 3 6-7" /></svg>
      </span>
    );
  }
  return (
    <span aria-hidden className="grid h-8 w-8 flex-none place-items-center rounded-lg" style={{ background: next ? "var(--accent)" : "var(--accent-soft)", color: next ? "var(--accent-ink)" : "var(--accent-text)" }}>
      <Icon name={STEP_ICONS[item.key] ?? "settings"} size={16} />
    </span>
  );
}

// ChecklistRows is the list itself: what's left first (the next step marked, with its own
// button), then what's done, quieter. Each row links to where that step is done; onOpen
// replaces plain navigation (the wizard finishes setup before leaving).
export function ChecklistRows({ items, onOpen, onChanged, onTested }: { items: ChecklistItem[]; onOpen?: (to: string) => void; onChanged?: () => void; onTested?: () => void }) {
  const todo = items.filter((it) => !it.done);
  const done = items.filter((it) => it.done);
  const nextKey = todo[0]?.key;
  return (
    <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
      {[...todo, ...done].map((it) => {
        if (it.action === "import_library") return <ImportRow key={it.key} item={it} onChanged={onChanged} />;
        const to = fixLink(it);
        const next = it.key === nextKey;
        const label = (
          <>
            <StepTile item={it} next={next} />
            <span className={`min-w-0 flex-1 ${it.done ? "text-ink-dim" : "font-medium"}`}>
              {it.label}
              {next && <span aria-hidden className="ml-2 rounded px-1.5 py-0.5 align-middle text-2xs font-bold uppercase tracking-wide" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>Next</span>}
            </span>
            <span className="sr-only">{it.done ? " (done)" : " (to do)"}</span>
          </>
        );
        return (
          <li key={it.key} className="rounded-lg" style={next ? { background: "var(--panel-2)", boxShadow: "inset 0 0 0 1px var(--accent-line)" } : undefined}>
            {to ? (
              <Link
                to={to}
                onClick={onOpen ? (e) => { e.preventDefault(); onOpen(to); } : undefined}
                className={`flex items-center gap-3 rounded-lg px-2.5 no-underline hover:bg-[var(--panel-2)] ${it.done ? "py-1 text-sm" : "py-2 text-base"}`}
                style={{ color: "var(--ink)" }}
              >
                {label}
                {!it.done && (
                  next
                    ? <span aria-hidden className="flex-none rounded-md px-2.5 py-1 text-sm font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>Set up</span>
                    : <span aria-hidden className="flex-none text-sm font-semibold" style={{ color: "var(--accent-text)" }}>→</span>
                )}
              </Link>
            ) : (
              <div className={`flex items-center gap-3 px-2.5 ${it.done ? "py-1 text-sm" : "py-2 text-base"}`}>{label}</div>
            )}
            <ChecklistProbe item={it} onTested={onTested} />
          </li>
        );
      })}
    </ul>
  );
}

// ImportRow is the step that runs itself: its row says how much is on disk against in
// Arrmada, and the import's button, progress and report sit under it.
function ImportRow({ item, onChanged }: { item: ChecklistItem; onChanged?: () => void }) {
  return (
    <li className="rounded-lg">
      <div className={`flex items-center gap-3 px-2.5 ${item.done ? "py-1 text-sm" : "py-2 text-base"}`}>
        <StepTile item={item} />
        <span className={`min-w-0 flex-1 ${item.done ? "text-ink-dim" : "font-medium"}`}>{item.label}</span>
        <span className="sr-only">{item.done ? " (done)" : " (to do)"}</span>
        {item.detail && <span className="text-sm text-ink-faint">{item.detail}</span>}
      </div>
      <div className="pl-[3.25rem] pr-2.5"><LibraryImport onChanged={onChanged} /></div>
    </li>
  );
}

// ChecklistProbe sits under a row whose connection is set up: the reason it isn't working (from
// the server's last probe, or from the Test just run) and an inline Test. It renders nothing
// for a step with nothing configured yet.
function ChecklistProbe({ item, onTested }: { item: ChecklistItem; onTested?: () => void }) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; detail: string } | null>(null);
  if (!item.test_kind || !item.test_ref) return null;
  const run = async () => {
    setBusy(true);
    try {
      const r = await connectionsApi.test(item.test_kind as ConnectionKind, item.test_ref as string);
      setResult({ ok: r.ok, detail: r.detail });
      onTested?.();
    } catch (e) {
      setResult({ ok: false, detail: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };
  const reason = result ? (result.ok ? "" : result.detail) : item.reason ?? "";
  return (
    <div className="flex flex-wrap items-center gap-2 pb-1.5 pl-[3.25rem] pr-2.5 text-sm">
      {reason && <span role="alert" className="min-w-0 flex-1" style={{ color: "var(--avoid-text)" }}>{reason}</span>}
      {result?.ok && <span role="status" style={{ color: "var(--good-text)" }}>✓ {result.detail || "Connected"}</span>}
      <Button variant="ghost" size="sm" onClick={run} busy={busy} aria-label={`Test ${item.label}`}>Test</Button>
    </div>
  );
}

// GettingStarted is the Dashboard card. It renders nothing once every step is done or the
// admin dismissed it, and nothing while it's loading or if the list can't be read.
export function GettingStarted() {
  const toast = useToast();
  const q = useSetupChecklist(true, { stopWhenDismissed: true });
  const [dismissing, setDismissing] = useState(false);
  const c = q.data;
  if (!c || c.done || c.dismissed) return null;
  const left = c.items.filter((it) => !it.done).length;

  const dismiss = async () => {
    setDismissing(true);
    try {
      q.mutate(await setupApi.dismissChecklist());
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setDismissing(false);
    }
  };

  const total = c.items.length;
  const pct = Math.round(((total - left) / Math.max(1, total)) * 100);
  return (
    <section aria-labelledby="getting-started" className="mb-6 overflow-hidden rounded-2xl" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)", boxShadow: "var(--shadow)" }}>
      <div className="px-5 pb-4 pt-5" style={{ background: "linear-gradient(160deg, var(--accent-soft), transparent 70%)" }}>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 id="getting-started" className="m-0 text-lg font-bold">Finish setting up Arrmada</h2>
            <p className="m-0 mt-0.5 text-sm text-ink-dim">{left === 1 ? "One step to go" : `${left} steps to go`} · {total - left} of {total} done. Each one ticks itself off when it's done.</p>
          </div>
          <Button variant="ghost" size="sm" onClick={dismiss} busy={dismissing} title="Hide this card. Settings → System → Run setup again still shows the list.">
            Dismiss
          </Button>
        </div>
        <div className="mt-3 h-1.5 overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }} role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={total - left} aria-label={`${left} to go`}>
          <div className="h-full rounded-full transition-[width] duration-500" style={{ width: `${pct}%`, background: "linear-gradient(90deg, var(--accent), var(--good))" }} />
        </div>
      </div>
      <div className="px-2.5 pb-3 pt-1">
        <ChecklistRows items={c.items} onChanged={q.refetch} onTested={() => void q.refetch()} />
        <p className="m-0 mt-2 px-2.5 text-sm text-ink-dim">
          Moving from Radarr or Sonarr? <Link to="/settings/import#arr-import" style={{ color: "var(--accent-text)" }}>Import your library, indexers and download clients</Link> — nothing in them changes.
        </p>
      </div>
    </section>
  );
}
