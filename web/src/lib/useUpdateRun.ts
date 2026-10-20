import { useCallback, useEffect, useRef, useState } from "react";
import { systemApi } from "./systemApi";

// Follows an update started with Update now from start to the new version answering.
//
// The app restarts part-way through, so the page can't just wait for one request: it asks
// the updater how it is going (through the app) until that stops answering, then asks the
// app's health until it answers with a different version, and reloads into it. If the app
// comes back on the same version the update ended without changing anything (nothing newer
// by the time it ran, or it went back by itself), and the page says so with the log.

// "unchanged" is the updater saying done while the app still answers with the version it had.
export type RunPhase = "idle" | "starting" | "running" | "restarting" | "unchanged" | "failed";

const POLL_MS = 2000;
// Longest the page keeps following one update. An install that compiles main can take the
// better part of an hour, but Update now only ever installs a published release (minutes).
const GIVE_UP_MS = 30 * 60 * 1000;

// phaseText is the one line shown for each phase.
export function phaseText(phase: RunPhase, currentVersion: string): string {
  switch (phase) {
    case "starting": return "Starting the update…";
    case "running": return "Updating: backing up the database and installing the new version…";
    case "restarting": return "Restarting Arrmada. This page reloads by itself when it's back.";
    case "unchanged": return `The updater finished, but Arrmada is still on ${currentVersion}. Nothing newer was installed, or it went back to this version; the log below says which.`;
    case "failed": return "The update didn't finish.";
    default: return "";
  }
}

export function useUpdateRun(currentVersion: string) {
  const [phase, setPhase] = useState<RunPhase>("idle");
  const [log, setLog] = useState("");
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const alive = useRef(true);
  const startedAt = useRef(0);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  const fail = useCallback((msg: string) => {
    if (!alive.current) return;
    setError(msg);
    setPhase("failed");
  }, []);

  const poll = useCallback(async () => {
    if (!alive.current) return;
    if (Date.now() - startedAt.current > GIVE_UP_MS) return fail("Gave up waiting after 30 minutes. Check the updater's log on the server.");
    let next = POLL_MS;
    try {
      const p = await systemApi.updateProgress();
      if (!alive.current) return;
      setLog(p.log);
      if (p.state === "failed") return fail("update.sh reported a problem; the log below says what. Arrmada goes back to the version it had if the new one didn't start.");
      if (p.state === "done") {
        // The updater only says done once the app answers: a new version means reload into
        // it, the same version means the update changed nothing.
        const h = await fetch("/api/health", { cache: "no-store" }).then((r) => r.json() as Promise<{ version?: string }>).catch(() => null);
        if (!alive.current) return;
        if (h?.version && h.version !== currentVersion) {
          window.location.reload();
          return;
        }
        if (h?.version) {
          setPhase("unchanged");
          return;
        }
        next = 500; // not answering yet
      }
      setPhase(p.state === "done" ? "restarting" : "running");
    } catch {
      // The app is restarting (or the updater is): look for it coming back.
      if (!alive.current) return;
      setPhase("restarting");
      try {
        const r = await fetch("/api/health", { cache: "no-store" });
        const h = (await r.json()) as { version?: string };
        if (r.ok && h.version && h.version !== currentVersion) {
          window.location.reload();
          return;
        }
      } catch {
        // still down
      }
    }
    // Back on the same version after the updater reported done: nothing changed.
    timer.current = setTimeout(() => void poll(), next);
  }, [currentVersion, fail]);

  const start = useCallback(async () => {
    setError(null);
    setLog("");
    setPhase("starting");
    startedAt.current = Date.now();
    try {
      await systemApi.applyUpdate();
    } catch (e) {
      return fail((e as Error).message);
    }
    if (!alive.current) return;
    setPhase("running");
    timer.current = setTimeout(() => void poll(), POLL_MS);
  }, [fail, poll]);

  return { phase, log, error, start, busy: phase === "starting" || phase === "running" || phase === "restarting" };
}
