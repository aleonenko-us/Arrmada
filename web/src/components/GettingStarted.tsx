import { useState } from "react";
import { Link } from "react-router-dom";
import { fixLink } from "../lib/links";
import { useQuery } from "../lib/query";
import { usePoll } from "../lib/usePoll";
import { setupApi, type ChecklistItem, type SetupChecklist } from "../lib/setupApi";
import { Button, useToast } from "../ui";
import { LibraryImport } from "./LibraryImport";

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

// ChecklistRows is the list itself: ✓ or ○, each a link to where that step is done.
// onOpen replaces plain navigation (the wizard finishes setup before leaving).
export function ChecklistRows({ items, onOpen, onChanged }: { items: ChecklistItem[]; onOpen?: (to: string) => void; onChanged?: () => void }) {
  return (
    <ul className="m-0 flex list-none flex-col gap-1 p-0">
      {items.map((it) => {
        if (it.action === "import_library") return <ImportRow key={it.key} item={it} onChanged={onChanged} />;
        const to = fixLink(it);
        const label = (
          <>
            <span aria-hidden className="w-4 flex-none text-center font-semibold" style={{ color: it.done ? "var(--good-text)" : "var(--ink-faint)" }}>{it.done ? "✓" : "○"}</span>
            <span className={it.done ? "text-ink-dim line-through decoration-[var(--ink-faint)]" : ""}>{it.label}</span>
            <span className="sr-only">{it.done ? " (done)" : " (to do)"}</span>
          </>
        );
        return (
          <li key={it.key}>
            {to ? (
              <Link
                to={to}
                onClick={onOpen ? (e) => { e.preventDefault(); onOpen(to); } : undefined}
                className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 text-base no-underline hover:bg-[var(--panel-2)]"
                style={{ color: "var(--ink)" }}
              >
                {label}
                {!it.done && <span aria-hidden className="ml-auto text-sm font-semibold" style={{ color: "var(--accent-text)" }}>→</span>}
              </Link>
            ) : (
              <div className="flex items-center gap-2.5 px-2 py-1.5 text-base">{label}</div>
            )}
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
    <li>
      <div className="flex items-center gap-2.5 px-2 py-1.5 text-base">
        <span aria-hidden className="w-4 flex-none text-center font-semibold" style={{ color: item.done ? "var(--good-text)" : "var(--ink-faint)" }}>{item.done ? "✓" : "○"}</span>
        <span className={item.done ? "text-ink-dim line-through decoration-[var(--ink-faint)]" : ""}>{item.label}</span>
        <span className="sr-only">{item.done ? " (done)" : " (to do)"}</span>
        {item.detail && <span className="ml-auto text-sm text-ink-faint">{item.detail}</span>}
      </div>
      <LibraryImport onChanged={onChanged} />
    </li>
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

  return (
    <section aria-labelledby="getting-started" className="mb-6">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h2 id="getting-started" className="m-0 font-mono text-2xs font-bold uppercase tracking-[0.12em] text-ink-faint">
          Getting started · {left} to go
        </h2>
        <Button variant="ghost" size="sm" onClick={dismiss} busy={dismissing} title="Hide this card. Settings → System → Run setup again still shows the list.">
          Dismiss
        </Button>
      </div>
      <div className="rounded-xl p-4" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
        <p className="m-0 mb-2 px-2 text-sm text-ink-dim">Each step ticks itself off once it's done.</p>
        <ChecklistRows items={c.items} onChanged={q.refetch} />
      </div>
    </section>
  );
}
