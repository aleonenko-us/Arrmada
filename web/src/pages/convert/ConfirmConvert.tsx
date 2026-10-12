import { useEffect, useState } from "react";
import { type ConvertAutoPreview, type ConvertRequestResult } from "../../lib/api";
import { convertApi } from "../../lib/convertApi";
import { ConfirmDialog } from "../../ui";
import { planChanges, previewLines } from "./plan";
import { usePlan } from "./PlanCard";

// Nothing on the Convert page replaces a file on one click. Convert now and Fix tracks say,
// line by line from the file's plan, what will change; a bulk show or season convert and
// Switch on say it in counts. Cancel leaves everything as it was.

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function Lines({ lines, warnings }: { lines: string[]; warnings?: string[] }) {
  return (
    <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
      {lines.map((l) => <li key={l} className="text-[12px] text-ink">• {l}</li>)}
      {warnings?.map((w) => <li key={w} className="text-[12px]" style={{ color: "var(--avoid)" }}>⚠ {w}</li>)}
    </ul>
  );
}

// ConvertNowDialog confirms converting (or only re-tidying the tracks of) one file now.
export function ConvertNowDialog({ itemKey, title, tracksOnly, originals, onCancel, onDone }: {
  itemKey: string; title: string; tracksOnly: boolean; originals: string;
  onCancel: () => void;
  /** Called with the request's answer once it's in; the caller closes the dialog. */
  onDone: (res: ConvertRequestResult) => void;
}) {
  const { data: plan, error: planError } = usePlan(itemKey);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = async () => {
    setBusy(true); setError(null);
    try { onDone(await convertApi.convertRequest(itemKey)); } catch (e) { setError(errText(e)); setBusy(false); }
  };
  return (
    <ConfirmDialog
      title={tracksOnly ? `Fix the tracks of “${title}” now?` : `Convert “${title}” now?`}
      tone="accent"
      confirmLabel={tracksOnly ? "Fix tracks" : "Convert now"}
      busyLabel="Asking…"
      busy={busy}
      confirmDisabled={!plan}
      error={error ?? (planError ? `Couldn't read the plan: ${planError.message}` : null)}
      onConfirm={() => void confirm()}
      onCancel={onCancel}
      body={
        <>
          {!plan && !planError && <p className="m-0 mt-1">Reading the plan…</p>}
          {plan && <Lines lines={planChanges(plan)} warnings={plan.warnings} />}
          <p className="m-0 mt-2.5 text-[11.5px] text-ink-faint">
            It runs whatever your hours, ahead of the automatic picks — an automatic conversion in progress waits for it. The file is replaced only once the result is checked. {originals}.
          </p>
        </>
      }
    />
  );
}

// usePreview reads the summary counts once, when the dialog opens.
function usePreview(seriesID?: number, season?: number) {
  const [preview, setPreview] = useState<ConvertAutoPreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    convertApi.convertAutoPreview(seriesID, season).then((p) => { if (live) setPreview(p); }).catch((e) => { if (live) setError(errText(e)); });
    return () => { live = false; };
  }, [seriesID, season]);
  return { preview, error };
}

// BulkConvertDialog confirms converting every episode of a show (or one season) that needs it.
export function BulkConvertDialog({ seriesID, season, label, originals, onCancel, onDone }: {
  seriesID: number; season?: number; label: string; originals: string;
  onCancel: () => void; onDone: (requested: number) => void;
}) {
  const { preview, error: loadError } = usePreview(seriesID, season);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = async () => {
    setBusy(true); setError(null);
    try { onDone((await convertApi.convertSeries(seriesID, season)).requested); } catch (e) { setError(errText(e)); setBusy(false); }
  };
  const lines = preview ? previewLines(preview) : [];
  return (
    <ConfirmDialog
      title={`Convert ${label} now?`}
      tone="accent"
      confirmLabel={preview ? `Convert ${preview.files.toLocaleString()} episode${preview.files === 1 ? "" : "s"}` : "Convert"}
      busyLabel="Asking…"
      busy={busy}
      confirmDisabled={!preview || preview.files === 0}
      error={error ?? (loadError ? `Couldn't count the episodes: ${loadError}` : null)}
      onConfirm={() => void confirm()}
      onCancel={onCancel}
      body={
        <>
          {!preview && !loadError && <p className="m-0 mt-1">Counting…</p>}
          {preview && preview.files === 0 && <p className="m-0 mt-1">Nothing in {label} needs converting.</p>}
          {lines.length > 0 && <Lines lines={lines} />}
          <p className="m-0 mt-2.5 text-[11.5px] text-ink-faint">
            They go ahead of the automatic picks and start as conversion slots free up, whatever your hours. {originals}.
          </p>
        </>
      }
    />
  );
}

// SwitchOnDialog is the first-run summary: what automatic conversion will do across the
// library. Auto is switched on only by Confirm (switchOn, which rejects when the save fails;
// the dialog then stays open with the error). Cancel changes nothing.
// deviceWarnings are sentences naming the players that had the formats Convert will write
// transcoded lately (the settings' devices panel, CONV-25); none = nothing to warn about.
export function SwitchOnDialog({ onCancel, switchOn, deviceWarnings = [] }: { onCancel: () => void; switchOn: () => Promise<void>; deviceWarnings?: string[] }) {
  const { preview, error: loadError } = usePreview();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = async () => {
    setBusy(true); setError(null);
    try { await switchOn(); } catch (e) { setError(errText(e)); setBusy(false); }
  };
  const lines = preview ? previewLines(preview) : [];
  return (
    <ConfirmDialog
      title="Switch on automatic conversion?"
      tone="accent"
      confirmLabel="Switch on"
      busyLabel="Switching on…"
      busy={busy}
      confirmDisabled={!preview && !loadError}
      error={error ?? (loadError ? `Couldn't count the library (${loadError}) — you can still switch on.` : null)}
      onConfirm={() => void confirm()}
      onCancel={onCancel}
      body={
        <>
          {!preview && !loadError && <p className="m-0 mt-1">Counting your library…</p>}
          {preview && preview.files === 0 && <p className="m-0 mt-1">Nothing in your library needs converting right now — new files are picked up as they arrive.</p>}
          {lines.length > 0 && (
            <>
              <p className="m-0 mt-1">Convert will work through your library during your hours, biggest savings first, replacing each file in place once its result is checked:</p>
              <Lines lines={lines} />
            </>
          )}
          {deviceWarnings.map((w) => <p key={w} className="m-0 mt-2 text-[11.5px]" style={{ color: "var(--avoid)" }}>⚠ {w}</p>)}
          <p className="m-0 mt-2.5 text-[11.5px] text-ink-faint">Counts are from the last library scan, so they can be a few minutes behind a settings change. Switching off again is one click.</p>
        </>
      }
    />
  );
}
