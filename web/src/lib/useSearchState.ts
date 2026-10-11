import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

export interface SearchParamOptions<T extends string> {
  /** Values the page offers; anything else in the address falls back to the default. */
  allowed?: readonly T[];
  /** Write the address this long after the last change (a search box), not on every key. */
  debounceMs?: number;
}

// readParam is the value a param holds, or the default when it's missing or not allowed.
export function readParam<T extends string>(raw: string | null, def: T, allowed?: readonly T[]): T {
  if (raw === null) return def;
  if (allowed && !(allowed as readonly string[]).includes(raw)) return def;
  return raw as T;
}

// withParam returns the query with one param set, dropping it when it's the default so a
// page's plain address is its default view. Every other param is kept.
export function withParam(params: URLSearchParams, key: string, value: string, def: string): URLSearchParams {
  const next = new URLSearchParams(params);
  if (value === def) next.delete(key);
  else next.set(key, value);
  return next;
}

// useSearchParamState is useState kept in the address (?filter=wanted&q=dune), so Back to
// a list brings its filter and search back, a reload keeps them, and the view can be
// shared as a link. Writes replace the history entry (typing doesn't pile up Back steps).
// With debounceMs the value updates at once for the page and the address follows once
// typing pauses; the address changing by itself (Back, a link) wins over what was typed.
export function useSearchParamState<T extends string>(key: string, def: T, opts: SearchParamOptions<T> = {}): [T, (v: T) => void] {
  const { allowed, debounceMs = 0 } = opts;
  const [params, setParams] = useSearchParams();
  const fromUrl = readParam(params.get(key), def, allowed);
  const [local, setLocal] = useState<T>(fromUrl);
  // The latest setter: a debounced write must start from the address as it is then, not
  // as it was when the timer was set, or it would undo a filter changed meanwhile.
  const setRef = useRef(setParams);
  setRef.current = setParams;
  const written = useRef(fromUrl);
  const timer = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    if (fromUrl !== written.current) {
      written.current = fromUrl;
      clearTimeout(timer.current);
      setLocal(fromUrl);
    }
  }, [fromUrl]);
  useEffect(() => () => clearTimeout(timer.current), []);

  const write = (v: T) => {
    written.current = v;
    setRef.current((p) => withParam(p, key, v, def), { replace: true });
  };
  const set = (v: T) => {
    const ok = readParam(v, def, allowed);
    setLocal(ok);
    clearTimeout(timer.current);
    if (debounceMs > 0) timer.current = setTimeout(() => write(ok), debounceMs);
    else write(ok);
  };
  return [debounceMs > 0 ? local : fromUrl, set];
}
