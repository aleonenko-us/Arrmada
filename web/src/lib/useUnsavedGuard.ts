import { useCallback, useEffect, useRef } from "react";
import { useBlocker, type BlockerFunction } from "react-router-dom";
import { useConfirm } from "../ui/Confirm";

// Unsaved edits: a form with a Save button calls useUnsavedGuard(dirty) and, while it's
// dirty, leaving it asks first — a sidebar link, Back, or the form's own Cancel that
// navigates away — and closing or reloading the tab gets the browser's own warning.
//
// React Router checks only one blocker per router, so the forms don't block themselves:
// they register here, and the one <UnsavedGuardHost/> in the staff layout blocks while any
// of them is dirty and asks with the latest one's question.

const DEFAULT_MESSAGE = "Discard your unsaved changes?";

const dirtyForms = new Map<symbol, string>();

/** The question for the most recently dirtied form, or null when nothing is dirty. */
export function unsavedMessage(): string | null {
  let last: string | null = null;
  for (const m of dirtyForms.values()) last = m;
  return last;
}

// Only a move to another page asks: a ?tab= or #anchor change keeps the form mounted.
export const leavesPage: BlockerFunction = ({ currentLocation, nextLocation }) =>
  unsavedMessage() !== null && currentLocation.pathname !== nextLocation.pathname;

/**
 * useUnsavedGuard keeps edits from being lost silently while dirty is true. It returns
 * markClean, for the form's own Save: call it right before navigating away after a save,
 * since dirty only turns false on the next render, after that navigation was checked.
 */
export function useUnsavedGuard(dirty: boolean, message: string = DEFAULT_MESSAGE): () => void {
  const id = useRef<symbol>();
  if (!id.current) id.current = Symbol("unsaved");
  const key = id.current;
  useEffect(() => {
    if (!dirty) return;
    dirtyForms.set(key, message);
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => {
      dirtyForms.delete(key);
      window.removeEventListener("beforeunload", warn);
    };
  }, [dirty, message, key]);
  return useCallback(() => { dirtyForms.delete(key); }, [key]);
}

/** Mounted once per layout, inside the router: asks before leaving a dirty form. */
export function UnsavedGuardHost(): null {
  const blocker = useBlocker(leavesPage);
  const confirm = useConfirm();
  const asking = useRef(false);
  useEffect(() => {
    if (blocker.state !== "blocked" || asking.current) return;
    asking.current = true;
    confirm({ title: unsavedMessage() ?? DEFAULT_MESSAGE, confirmLabel: "Discard", cancelLabel: "Keep editing", tone: "danger" })
      .then((discard) => {
        asking.current = false;
        if (discard) {
          // The form unmounts with the navigation; drop its claim now so nothing re-asks.
          dirtyForms.clear();
          blocker.proceed();
        } else {
          blocker.reset();
        }
      });
  }, [blocker, confirm]);
  return null;
}
