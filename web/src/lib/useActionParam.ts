import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";

// useActionParam does what a link asked for once (/movies?add=1 from the command palette
// opens the Add dialog), then drops the parameter so a reload or Back doesn't do it again.
export function useActionParam(name: string, run: () => void): void {
  const [params, setParams] = useSearchParams();
  const wanted = params.get(name) === "1";
  const runRef = useRef(run);
  useEffect(() => { runRef.current = run; });
  useEffect(() => {
    if (!wanted) return;
    runRef.current();
    setParams((p) => {
      const next = new URLSearchParams(p);
      next.delete(name);
      return next;
    }, { replace: true });
  }, [wanted, name, setParams]);
}
