import { useState } from "react";
import type { SystemStatus, UpdateStatus } from "../../lib/api";
import { setQueryData } from "../../lib/query";
import { systemApi } from "../../lib/systemApi";
import { ago } from "../../lib/taskTime";
import { noteBlocks, updateHeadline, updateHint } from "../../lib/updateStatus";
import { useAutoSave } from "../../lib/useAutoSave";
import { Button } from "../../ui";
import { AutoToggle } from "../settings/auto";

const TONE = { update: "var(--accent-text)", ok: "var(--good-text)", muted: "var(--ink-dim)" } as const;

// UpdateCheck is the Updates card on Settings → Status: whether a newer release is out
// (or, for a build of main, how far behind main it is), how this server gets it, the
// release notes, Check now and the switch. The check only ever reads GitHub's public
// releases; the hint says so.
export function UpdateCheck({ update }: { update: UpdateStatus }) {
  const [checking, setChecking] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const store = (u: UpdateStatus) => setQueryData<SystemStatus | undefined>("system:status", (old) => (old ? { ...old, update: u } : old));

  const enabled = useAutoSave(update.enabled, async (v: boolean) => {
    let u = await systemApi.setUpdateCheck(v);
    // Switched on: ask straight away rather than leave it at "Not checked yet".
    if (v) u = await systemApi.checkForUpdate().catch(() => u);
    store(u);
  });
  const checkNow = async () => {
    setChecking(true); setErr(null);
    try { store(await systemApi.checkForUpdate()); } catch (e) { setErr((e as Error).message); } finally { setChecking(false); }
  };

  const head = updateHeadline(update);
  const hint = updateHint(update);
  const latest = update.enabled ? update.latest : null;
  return (
    <section id="updates" className="scroll-mt-20 rounded-xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <h2 className="m-0 text-md font-bold">Updates</h2>
      <p className="mb-4 mt-0.5 text-sm text-ink-faint">Whether a newer Arrmada is out, and what changed.</p>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <span className="text-md font-semibold" style={{ color: TONE[head.tone] }}>{head.text}</span>
          {update.enabled && (
            <span className="text-xs text-ink-faint">{update.checked_at ? `Checked ${ago(update.checked_at)}` : ""}</span>
          )}
        </div>
        {update.enabled && update.problem && <p className="m-0 text-sm text-ink-dim">{update.problem}</p>}
        {hint && (
          <div className="flex flex-col gap-1">
            <p className="m-0 text-sm text-ink-dim">{hint.text}</p>
            <code className="block overflow-x-auto whitespace-nowrap rounded-md px-2 py-1 font-mono text-xs" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>{hint.command}</code>
          </div>
        )}
        {latest && <ReleaseNotes release={latest} open={update.available} label={update.available ? "What changed" : `Newest release: ${latest.tag}`} />}
        {err && <p role="alert" className="m-0 text-sm" style={{ color: "var(--reject-text, var(--reject))" }}>{err}</p>}
        <div className="flex flex-wrap items-center gap-3">
          <Button size="sm" onClick={() => void checkNow()} busy={checking} busyLabel="Checking…" disabled={!update.enabled}>Check now</Button>
        </div>
        <AutoToggle
          label="Check for updates"
          hint="Reads GitHub's public list of Arrmada releases every 12 hours. Nothing about this server is sent."
          save={enabled}
        />
      </div>
    </section>
  );
}

// ReleaseNotes shows a release's notes as text, folded unless it's the update on offer.
function ReleaseNotes({ release, open, label }: { release: NonNullable<UpdateStatus["latest"]>; open: boolean; label: string }) {
  const blocks = noteBlocks(release.notes);
  return (
    <details open={open} className="rounded-lg px-3 py-2" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <summary className="cursor-pointer text-sm font-semibold">{label}</summary>
      <div className="mt-2 flex flex-col gap-1 text-sm text-ink-dim">
        {blocks.length === 0 && <p className="m-0">This release has no notes.</p>}
        {blocks.map((b, i) =>
          b.kind === "heading" ? <p key={i} className="m-0 mt-1 font-semibold text-ink">{b.text}</p>
            : b.kind === "bullet" ? <p key={i} className="m-0 pl-3" style={{ textIndent: "-0.75rem" }}>• {b.text}</p>
              : <p key={i} className="m-0">{b.text}</p>,
        )}
        {release.notes_truncated && <p className="m-0 text-ink-faint">…</p>}
        {release.url && (
          <a href={release.url} target="_blank" rel="noreferrer noopener" className="mt-1 text-sm font-semibold" style={{ color: "var(--accent-text)" }}>
            {release.tag} on GitHub →
          </a>
        )}
      </div>
    </details>
  );
}
