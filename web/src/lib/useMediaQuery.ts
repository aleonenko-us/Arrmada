import { useEffect, useState } from "react";

function matches(query: string): boolean {
  try { return typeof window !== "undefined" && window.matchMedia(query).matches; } catch { return false; }
}

// useMediaQuery follows a CSS media query. The first value is read synchronously, so a
// phone never renders the desktop layout for a frame; it updates when the window is
// resized or rotated.
export function useMediaQuery(query: string): boolean {
  const [on, setOn] = useState(() => matches(query));
  useEffect(() => {
    let mq: MediaQueryList;
    try { mq = window.matchMedia(query); } catch { return; }
    const onChange = () => setOn(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [query]);
  return on;
}

// Tailwind's sm breakpoint: below it is a phone.
export const PHONE = "(max-width: 639.98px)";
