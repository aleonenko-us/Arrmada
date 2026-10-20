import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button, IconButton, Modal } from "../../ui";
import { useQuery } from "../../lib/query";
import { seriesApi, type MisplacedItem, type MisplacedPlan, type MisplacedResult } from "../../lib/seriesApi";

const base = (p: string) => p.slice(Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\")) + 1);
const dir = (p: string) => p.slice(0, Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\")));
const ep = (it: MisplacedItem) => `S${String(it.season).padStart(2, "0")}E${String(it.episode).padStart(2, "0")}`;
const files = (n: number) => `${n} file${n === 1 ? "" : "s"}`;

// MisplacedBanner says when some of the show's episode files sit outside its folder — in a
// folder named like a season right inside the TV folder (<TV>/S13/Season 12), where imports
// went before #56 was fixed — and opens the preview to put them back. A health check links
// here with ?fix=misplaced, which opens the preview straight away.
export function MisplacedBanner({ seriesId, title, refreshKey, onChange }: { seriesId: number; title: string; refreshKey?: number; onChange: () => void }) {
  const q = useQuery(`series-misplaced:${seriesId}`, () => seriesApi.misplacedPreview(seriesId), { staleMs: 0 });
  const { refetch } = q;
  const mounted = useRef(false);
  useEffect(() => {
    if (!mounted.current) { mounted.current = true; return; }
    void refetch();
  }, [refreshKey, refetch]);
  const [params, setParams] = useSearchParams();
  const asked = params.get("fix") === "misplaced";
  const [open, setOpen] = useState(asked);
  const close = () => {
    setOpen(false);
    if (asked) setParams((p) => { const n = new URLSearchParams(p); n.delete("fix"); return n; }, { replace: true });
  };
  const items = q.data?.items ?? [];
  if (items.length === 0 && !open) return null;
  return (
    <>
      {items.length > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-3 rounded-lg px-3 py-2.5 text-base" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)" }}>
          <span className="min-w-0 flex-1" style={{ color: "var(--ink)" }}>
            {files(items.length)} of this show {items.length === 1 ? "is" : "are"} outside its folder, in a folder named like a season.
            {q.data?.new_folder ? " New episodes are held for review until they're put back." : ""}
          </span>
          <Button size="sm" variant="primary" onClick={() => setOpen(true)}>Review</Button>
        </div>
      )}
      {open && <MisplacedModal seriesId={seriesId} title={title} onClose={close} onDone={() => { void refetch(); onChange(); }} />}
    </>
  );
}

// MisplacedModal lists every misplaced file and what putting it back would do before
// anything moves. Nothing is ever replaced: where the show's folder already has the
// episode, the misplaced copy goes to the recycle bin only when it's the same or worse,
// and otherwise both stay for you to choose. Confirm sends back exactly what was shown.
export function MisplacedModal({ seriesId, title, onClose, onDone }: { seriesId: number; title: string; onClose: () => void; onDone: () => void }) {
  const titleId = useId();
  const [plan, setPlan] = useState<MisplacedPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<MisplacedResult | null>(null);

  useEffect(() => {
    let alive = true;
    seriesApi.misplacedPreview(seriesId)
      .then((p) => { if (alive) setPlan(p); })
      .catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [seriesId]);

  const groups = useMemo(() => {
    const items = plan?.items ?? [];
    return {
      move: items.filter((it) => it.action === "move"),
      recycle: items.filter((it) => it.action === "recycle"),
      leave: items.filter((it) => it.action === "leave"),
    };
  }, [plan]);
  const doing = [...groups.move, ...groups.recycle];

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      setResult(await seriesApi.putMisplacedBack(seriesId, doing));
      onDone();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="xl">
      <div className="mb-1 flex items-center justify-between">
        <h2 id={titleId} className="m-0 text-lg font-bold">Put misplaced files back</h2>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>
      <p className="mb-3 text-sm text-ink-dim">
        Episode files of <b>{title}</b> that were imported into a folder named like a season instead of the show's own folder.
        Nothing moves until you confirm, and no file is ever replaced.
      </p>
      {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}

      {result ? (
        <div>
          <div className="mb-3 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)", color: "var(--accent-text)" }}>
            Moved {files(result.moved)} back{result.recycled > 0 ? ` · ${files(result.recycled)} sent to the recycle bin` : ""}
            {result.skipped.length > 0 ? ` · skipped ${result.skipped.length}` : ""}.
            {result.removed_folders.length > 0 && ` Removed ${result.removed_folders.length} empty folder${result.removed_folders.length === 1 ? "" : "s"}.`}
          </div>
          {result.skipped.length > 0 && (
            <div className="thin-scroll max-h-[40vh] overflow-y-auto">
              {result.skipped.map((sk) => (
                <div key={sk.from} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
                  <div className="truncate font-mono text-xs" title={sk.from}>{base(sk.from)}</div>
                  <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>{sk.reason}</div>
                </div>
              ))}
            </div>
          )}
          <div className="mt-4 flex justify-end">
            <Button variant="secondary" onClick={onClose}>Close</Button>
          </div>
        </div>
      ) : plan === null ? (
        !error && <div className="p-6 text-center text-base text-ink-dim">Looking for misplaced files…</div>
      ) : plan.items.length === 0 ? (
        <div className="p-6 text-center text-base text-ink-dim">Every file of this show is in its own folder.</div>
      ) : (
        <>
          {plan.why ? (
            <div className="mb-3 text-sm" style={{ color: "var(--reject-text)" }}>{plan.why}</div>
          ) : (
            <div className="mb-2 text-sm text-ink-dim">
              Into <span className="font-mono text-xs" style={{ color: "var(--ink)" }}>{plan.show_folder}</span>
              {plan.new_folder ? " — a new folder, named by your naming scheme, because none of the show's files are in a folder of its own." : ""}
            </div>
          )}
          <div className="thin-scroll max-h-[52vh] overflow-y-auto">
            <Group label={`${files(groups.move.length)} to move back`} items={groups.move} render={(it) => (
              <>
                <div className="truncate font-mono text-xs text-ink-faint" title={it.from}>{ep(it)} · {dir(it.from)}</div>
                <div className="truncate font-mono text-xs" title={it.to}>→ {it.to ? dir(it.to) : ""}/{base(it.from)}</div>
              </>
            )} />
            <Group label={`${files(groups.recycle.length)} already in the show's folder — the misplaced copy goes to the recycle bin`} items={groups.recycle} render={(it) => (
              <>
                <div className="truncate font-mono text-xs text-ink-faint" title={it.from}>{ep(it)} · {it.from}</div>
                <div className="mt-0.5 text-xs text-ink-dim">{it.note}</div>
              </>
            )} />
            <Group label={`${files(groups.leave.length)} left for you`} items={groups.leave} render={(it) => (
              <>
                <div className="truncate font-mono text-xs text-ink-faint" title={it.from}>{ep(it)} · {it.from}</div>
                <div className="mt-0.5 text-xs" style={{ color: "var(--reject-text)" }}>{it.note}</div>
              </>
            )} />
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={onClose}>Cancel</Button>
            <Button variant="primary" onClick={confirm} disabled={doing.length === 0} busy={busy} busyLabel="Putting back…">
              {`Put back ${files(doing.length)}`}
            </Button>
          </div>
        </>
      )}
    </Modal>
  );
}

function Group({ label, items, render }: { label: string; items: MisplacedItem[]; render: (it: MisplacedItem) => React.ReactNode }) {
  if (items.length === 0) return null;
  return (
    <div className="mb-3">
      <div className="mb-1 text-xs font-semibold text-ink-dim">{label}</div>
      {items.map((it) => (
        <div key={it.from} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>{render(it)}</div>
      ))}
    </div>
  );
}
