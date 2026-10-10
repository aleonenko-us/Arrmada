import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";
import { useToast } from "../ui/Toast";

// useAutoSave is how one control saves itself: no Save button, no page-wide dirty state.
// It works with any endpoint, not just /settings — give it the value the server last
// confirmed and a function that stores a new one.
//
//   const name = useAutoSave(cfg.name, (v) => api.rename(v).then(setCfg));
//   <input value={name.value} onChange={(e) => name.set(e.target.value)} onBlur={() => name.commit()} />
//   <SavedTick save={name} />
//
// - set() changes only what's on screen (a draft); nothing is sent until commit().
//   Text fields commit on blur or Enter, toggles and selects commit on change, so a
//   half-typed value never reaches the server's validation.
// - commit() sends nothing when the value equals the saved one, and nothing when
//   `validate` objects (the message shows as a field error instead).
// - `save` must update whatever `saved` comes from (component state, setQueryData, the
//   settings snapshot) before its promise resolves: the draft is dropped on success and
//   the control falls back to `saved`.
// - A failure keeps a typed value on screen so it can be fixed or retried; a boolean (a
//   toggle) flips back to what is really stored. keepOnError overrides either way.
//   Retry is offered for network and server errors, not for a 4xx the user must fix.
// - Saves from one control run one after another, and only the newest decides the
//   status, so flipping a toggle twice quickly ends with the second value stored and
//   shown, never the first.
// - While a draft is unsaved the browser warns before the tab is closed. A save that
//   fails after the control is gone (the user navigated away mid-save) becomes a toast.

export type SaveStatus = "idle" | "saving" | "saved" | "error";

export interface AutoSave<T> {
  /** What the control shows: the draft while there is one, otherwise the saved value. */
  value: T;
  /** A local edit is waiting to be committed (or failed to save). */
  dirty: boolean;
  set: (v: T) => void;
  /** Save v (default: the current draft). Resolves true when it's stored or unchanged. */
  commit: (v?: T) => Promise<boolean>;
  /** Drop the draft and show the saved value again (Esc). */
  revert: () => void;
  /** Send the last failed value again. Only meaningful when canRetry. */
  retry: () => void;
  status: SaveStatus;
  error: string | null;
  canRetry: boolean;
}

export interface AutoSaveOptions<T> {
  /** A message when v can't be saved as it is; checked before anything is sent. */
  validate?: (v: T) => string | null;
  equal?: (a: T, b: T) => boolean;
  /** Keep a value that failed to save on screen (default: yes, except for booleans). */
  keepOnError?: boolean;
  /** How long "Saved ✓" stays, in ms. */
  savedMs?: number;
}

const SAVED_MS = 2000;

// A 4xx is the server saying the value is wrong: sending it again won't help.
function retryable(e: unknown): boolean {
  return !(e instanceof ApiError && e.status >= 400 && e.status < 500);
}

export function useAutoSave<T>(saved: T, save: (v: T) => Promise<unknown>, opts: AutoSaveOptions<T> = {}): AutoSave<T> {
  const { validate, keepOnError = typeof saved !== "boolean", savedMs = SAVED_MS } = opts;
  const equal = opts.equal ?? Object.is;
  const toast = useToast();
  const [draft, setDraft] = useState<{ v: T } | null>(null);
  const [status, setStatus] = useState<SaveStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const [canRetry, setCanRetry] = useState(false);

  // Refs so commit() called from a blur handler sees this render's values, and a late
  // answer knows whether it's still the newest and whether anyone is still looking.
  const seq = useRef(0);
  const failed = useRef<{ v: T } | null>(null);
  const chain = useRef<Promise<unknown>>(Promise.resolve());
  const inFlight = useRef(0);
  const lastSent = useRef<{ v: T; seq: number } | null>(null);
  const mounted = useRef(true);
  const timer = useRef<number | undefined>(undefined);
  const latest = useRef({ saved, draft, save, validate, equal });
  latest.current = { saved, draft, save, validate, equal };
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; window.clearTimeout(timer.current); };
  }, []);

  const commit = useCallback(async (next?: T): Promise<boolean> => {
    const cur = latest.current;
    const v = next !== undefined ? next : cur.draft ? cur.draft.v : cur.saved;
    window.clearTimeout(timer.current);
    const invalid = cur.validate?.(v) ?? null;
    if (invalid) {
      seq.current++; // an older save still in flight no longer speaks for this field
      setDraft({ v });
      setStatus("error"); setError(invalid); setCanRetry(false);
      failed.current = null;
      return false;
    }
    // "Unchanged" is only trustworthy with nothing in flight: mid-save, `saved` is still
    // the old value, and going back to it has to be sent to undo the first save.
    if (inFlight.current === 0 && cur.equal(v, cur.saved)) {
      seq.current++;
      setDraft(null);
      setStatus("idle"); setError(null); setCanRetry(false);
      return true;
    }
    // Enter then blur commits the same value twice; the second rides on the first.
    if (inFlight.current > 0 && lastSent.current && cur.equal(v, lastSent.current.v) && seq.current === lastSent.current.seq) return true;
    const mine = ++seq.current;
    lastSent.current = { v, seq: mine };
    setDraft({ v });
    setStatus("saving"); setError(null); setCanRetry(false);
    inFlight.current++;
    const run = chain.current.then(() => cur.save(v));
    chain.current = run.catch(() => {});
    try {
      await run;
      if (mine !== seq.current || !mounted.current) return true;
      failed.current = null;
      // Edited again while this was saving: keep the newer draft for its own commit.
      setDraft((d) => (d && !latest.current.equal(d.v, v) ? d : null));
      setStatus("saved");
      timer.current = window.setTimeout(() => { if (mounted.current) setStatus((s) => (s === "saved" ? "idle" : s)); }, savedMs);
      return true;
    } catch (e) {
      const msg = (e as Error).message || "Couldn't save";
      if (!mounted.current) {
        toast(`Couldn't save: ${msg}`, { tone: "error", ms: 6000 });
        return false;
      }
      if (mine !== seq.current) return false;
      failed.current = { v };
      setStatus("error"); setError(msg); setCanRetry(retryable(e));
      if (!keepOnError) setDraft(null);
      return false;
    } finally {
      inFlight.current--;
    }
  }, [keepOnError, savedMs, toast]);

  const revert = useCallback(() => {
    seq.current++;
    window.clearTimeout(timer.current);
    failed.current = null;
    setDraft(null);
    setStatus("idle"); setError(null); setCanRetry(false);
  }, []);

  const retry = useCallback(() => {
    if (failed.current) void commit(failed.current.v);
  }, [commit]);

  const set = useCallback((v: T) => setDraft({ v }), []);

  const dirty = draft !== null && !equal(draft.v, saved);
  // Closing the tab with an edit that hasn't saved would lose it without a word.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  return { value: draft ? draft.v : saved, dirty, set, commit, revert, retry, status, error, canRetry };
}
