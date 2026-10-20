import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

export type ToastTone = "info" | "good" | "error";
export interface ToastOptions {
  tone?: ToastTone;
  /** How long it stays up, in ms. */
  ms?: number;
}
export type ToastFn = (msg: string, opts?: ToastOptions) => void;

interface Item { id: number; msg: string; tone: ToastTone }

// Each tone's tint, border and icon badge, from the theme's tokens (light and dark alike).
const TONES: Record<ToastTone, { soft: string; line: string; badge: string }> = {
  info: { soft: "var(--accent-soft)", line: "var(--accent-line)", badge: "var(--accent)" },
  good: { soft: "var(--good-soft)", line: "var(--good)", badge: "var(--good)" },
  error: { soft: "var(--reject-soft)", line: "var(--reject)", badge: "var(--reject)" },
};

function ToneIcon({ tone }: { tone: ToastTone }) {
  const p = { viewBox: "0 0 16 16", width: 14, height: 14, fill: "none", stroke: "currentColor", strokeWidth: 2.2, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  if (tone === "good") return <svg {...p}><path d="M3.5 8.5l3 3 6-7" /></svg>;
  if (tone === "error") return <svg {...p}><path d="M8 3.5v5.5M8 12.2v.3" /></svg>;
  return <svg {...p}><path d="M8 7.2v5M8 3.8v.3" /></svg>;
}

// At most this many on screen; a burst of failures shouldn't wallpaper the page.
const MAX = 3;

const ToastCtx = createContext<ToastFn | null>(null);

// ToastProvider owns the one live region every page's short confirmations and
// failures go through. It sits above the requester bottom bar (--bottom-chrome),
// stacks up to three, and each message clears on its own timer — a second toast
// no longer cuts the first one short.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Item[]>([]);
  const next = useRef(0);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    const t = timers.current.get(id);
    if (t) clearTimeout(t);
    timers.current.delete(id);
    setItems((xs) => xs.filter((x) => x.id !== id));
  }, []);

  const toast = useCallback<ToastFn>((msg, opts) => {
    const id = ++next.current;
    const tone = opts?.tone ?? "info";
    setItems((xs) => {
      const keep = [...xs, { id, msg, tone }];
      // Drop the oldest beyond the cap, and its timer with it.
      for (const old of keep.slice(0, Math.max(0, keep.length - MAX))) {
        const t = timers.current.get(old.id);
        if (t) clearTimeout(t);
        timers.current.delete(old.id);
      }
      return keep.slice(-MAX);
    });
    // Errors stay longer: they usually need reading.
    timers.current.set(id, setTimeout(() => dismiss(id), opts?.ms ?? (tone === "error" ? 6000 : 3500)));
  }, [dismiss]);

  useEffect(() => {
    const map = timers.current;
    return () => { map.forEach(clearTimeout); map.clear(); };
  }, []);

  return (
    <ToastCtx.Provider value={toast}>
      {children}
      {createPortal(
        <div
          aria-live="polite"
          className="pointer-events-none fixed left-1/2 z-toast flex w-max max-w-[calc(100vw-2rem)] -translate-x-1/2 flex-col items-center gap-2 font-sans"
          style={{ bottom: "calc(var(--bottom-chrome) + 20px)" }}
        >
          {items.map((t) => {
            const look = TONES[t.tone];
            return (
              <div
                key={t.id}
                role={t.tone === "error" ? "alert" : "status"}
                className="toast-in pointer-events-auto flex w-[min(26rem,calc(100vw-2rem))] items-start gap-3 rounded-xl py-3 pl-3 pr-2 text-base"
                style={{
                  // The tone's tint laid over the solid panel, so the page never shows through.
                  background: `linear-gradient(${look.soft}, ${look.soft}), var(--panel)`,
                  border: `1px solid ${look.line}`,
                  boxShadow: "var(--shadow)",
                  color: "var(--ink)",
                }}
              >
                <span aria-hidden className="grid h-7 w-7 shrink-0 place-items-center rounded-full" style={{ background: look.badge, color: "var(--accent-ink)" }}>
                  <ToneIcon tone={t.tone} />
                </span>
                <span className="min-w-0 flex-1 self-center break-words font-medium leading-snug">{t.msg}</span>
                <button
                  type="button"
                  onClick={() => dismiss(t.id)}
                  aria-label="Dismiss"
                  className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-ink-faint hover:text-ink"
                >
                  <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"><path d="M4 4l8 8M12 4l-8 8" /></svg>
                </button>
              </div>
            );
          })}
        </div>,
        document.body,
      )}
    </ToastCtx.Provider>
  );
}

// useToast returns toast(msg, {tone, ms}). Outside a provider (an isolated test)
// it's a no-op rather than a crash.
export function useToast(): ToastFn {
  return useContext(ToastCtx) ?? noop;
}
const noop: ToastFn = () => {};
