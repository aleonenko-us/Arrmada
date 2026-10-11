import { useEffect, useRef, useSyncExternalStore } from "react";
import { SIGNED_OUT_EVENT } from "./api";

// The tab's one websocket. Every page, badge and job follower listens on it instead of
// opening its own (each useLive used to open a socket, so a staff tab held four or five),
// and pages refetch when the server says something moved instead of polling blind.
//
//  - It opens when the first listener arrives and closes a few seconds after the last
//    one leaves (a page change doesn't drop it).
//  - A dropped connection retries with backoff, 1 s doubling to 30 s; a reconnect tells
//    the reconnect listeners, so pages catch up on whatever they missed.
//  - A tab hidden for a minute closes it: the server stops reading the download queue for
//    nobody, and the tab catches up the moment it's shown again.
//  - Sign-out closes it for good; the next signed-in shell starts a fresh one.
//
// What a socket carries is decided server-side (internal/realtime/policy.go): staff get
// the library's topics, everyone gets server.heartbeat and their own user.<id>.* events.
// This file stays tiny and free of the query cache: the requester shell loads it.

export interface LiveEvent {
  topic: string;
  data: unknown;
}

type Handler = (ev: LiveEvent) => void;
interface Sub { patterns: string[]; fn: Handler }

export const BACKOFF_MIN_MS = 1000;
export const BACKOFF_MAX_MS = 30_000;
export const IDLE_CLOSE_MS = 5000;
export const HIDDEN_CLOSE_MS = 60_000;

const subs = new Set<Sub>();
const reconnectFns = new Set<() => void>();
const stateFns = new Set<() => void>();
let ws: WebSocket | null = null;
let connected = false;
let opened = false; // has this session ever connected (so the next open is a reconnect)
let backoff = BACKOFF_MIN_MS;
let retry: ReturnType<typeof setTimeout> | undefined;
let idleTimer: ReturnType<typeof setTimeout> | undefined;
let hiddenTimer: ReturnType<typeof setTimeout> | undefined;
let hiddenClosed = false;
let signedOut = false;

/**
 * topicMatches: "*" matches anything; a "*" segment matches one segment, and a trailing
 * one matches the rest ("movie.*" matches "movie.search.done", "user.*.request.updated"
 * matches "user.7.request.updated").
 */
export function topicMatches(pattern: string, topic: string): boolean {
  if (pattern === "*" || pattern === topic) return true;
  const p = pattern.split(".");
  const t = topic.split(".");
  for (let i = 0; i < p.length; i++) {
    if (p[i] === "*" && i === p.length - 1) return t.length >= p.length;
    if (i >= t.length || (p[i] !== "*" && p[i] !== t[i])) return false;
  }
  return p.length === t.length;
}

const users = () => subs.size + reconnectFns.size + stateFns.size;

function setConnected(v: boolean) {
  if (connected === v) return;
  connected = v;
  stateFns.forEach((fn) => fn());
}

function dispatch(ev: LiveEvent) {
  for (const s of [...subs]) {
    if (!s.patterns.some((p) => topicMatches(p, ev.topic))) continue;
    try { s.fn(ev); } catch { /* one broken listener mustn't starve the rest */ }
  }
}

function open() {
  if (ws || retry || signedOut || hiddenClosed || users() === 0 || typeof WebSocket === "undefined") return;
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  let sock: WebSocket;
  try {
    sock = new WebSocket(`${proto}://${window.location.host}/api/v1/ws`);
  } catch {
    scheduleRetry();
    return;
  }
  ws = sock;
  sock.onopen = () => {
    if (ws !== sock) return;
    backoff = BACKOFF_MIN_MS;
    setConnected(true);
    if (opened) reconnectFns.forEach((fn) => { try { fn(); } catch { /* keep going */ } });
    opened = true;
  };
  sock.onmessage = (e) => {
    if (ws !== sock) return;
    let ev: LiveEvent;
    try { ev = JSON.parse(String(e.data)) as LiveEvent; } catch { return; }
    if (ev && typeof ev.topic === "string") dispatch(ev);
  };
  sock.onclose = () => {
    if (ws !== sock) return; // closed on purpose (close() let go of it first)
    ws = null;
    setConnected(false);
    scheduleRetry();
  };
}

function scheduleRetry() {
  if (retry || signedOut || hiddenClosed || users() === 0) return;
  retry = setTimeout(() => { retry = undefined; open(); }, backoff);
  backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
}

function close() {
  clearTimeout(retry);
  retry = undefined;
  const sock = ws;
  ws = null;
  setConnected(false);
  try { sock?.close(); } catch { /* already gone */ }
}

function startHiddenTimer() {
  clearTimeout(hiddenTimer);
  hiddenTimer = setTimeout(() => { hiddenTimer = undefined; hiddenClosed = true; close(); }, HIDDEN_CLOSE_MS);
}

function addUser() {
  // Sign-out tears the shell down; the next listener after that belongs to a new session.
  if (signedOut && users() === 0) signedOut = false;
  clearTimeout(idleTimer);
  idleTimer = undefined;
  // A tab opened in the background never fires visibilitychange; start its clock here.
  if (typeof document !== "undefined" && document.visibilityState === "hidden" && !hiddenTimer && !hiddenClosed) startHiddenTimer();
}

function removeUser() {
  if (users() > 0) return;
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => { idleTimer = undefined; if (users() === 0) close(); }, IDLE_CLOSE_MS);
}

if (typeof window !== "undefined") {
  window.addEventListener(SIGNED_OUT_EVENT, () => {
    signedOut = true;
    opened = false;
    backoff = BACKOFF_MIN_MS;
    close();
  });
}
if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      startHiddenTimer();
      return;
    }
    clearTimeout(hiddenTimer);
    hiddenTimer = undefined;
    if (hiddenClosed) {
      hiddenClosed = false;
      backoff = BACKOFF_MIN_MS;
      open();
    }
  });
}

/** Listen for topics (see topicMatches); returns the unsubscribe. Opens the socket if needed. */
export function subscribe(patterns: string[], fn: Handler): () => void {
  addUser();
  const s: Sub = { patterns, fn };
  subs.add(s);
  open();
  return () => {
    subs.delete(s);
    removeUser();
  };
}

/** Called each time the socket comes back after a drop: refetch what may have been missed. */
export function onReconnect(fn: () => void): () => void {
  addUser();
  reconnectFns.add(fn);
  open();
  return () => {
    reconnectFns.delete(fn);
    removeUser();
  };
}

function subscribeState(fn: () => void): () => void {
  addUser();
  stateFns.add(fn);
  open();
  return () => {
    stateFns.delete(fn);
    removeUser();
  };
}

/** Whether the tab's socket is up right now (Dashboard's "Realtime: connected"). */
export function useLiveConnected(): boolean {
  return useSyncExternalStore(subscribeState, () => connected, () => false);
}

/**
 * useLiveEvent calls handler for every event matching patterns while the component is
 * mounted (and enabled). The handler may change every render; patterns are compared by
 * value.
 */
export function useLiveEvent(patterns: string | readonly string[], handler: Handler, enabled = true): void {
  const ref = useRef(handler);
  useEffect(() => { ref.current = handler; });
  const key = typeof patterns === "string" ? patterns : patterns.join(" ");
  useEffect(() => {
    if (!enabled || !key) return;
    return subscribe(key.split(" "), (ev) => ref.current(ev));
  }, [key, enabled]);
}

/** useOnReconnect runs fn each time the socket comes back after a drop. */
export function useOnReconnect(fn: () => void, enabled = true): void {
  const ref = useRef(fn);
  useEffect(() => { ref.current = fn; });
  useEffect(() => {
    if (!enabled) return;
    return onReconnect(() => ref.current());
  }, [enabled]);
}

/** Test hook: forget every listener and the socket, as on a fresh page load. */
export function resetLiveForTests() {
  subs.clear();
  reconnectFns.clear();
  stateFns.clear();
  close();
  clearTimeout(idleTimer);
  clearTimeout(hiddenTimer);
  idleTimer = hiddenTimer = undefined;
  opened = hiddenClosed = signedOut = false;
  backoff = BACKOFF_MIN_MS;
  connected = false;
}
