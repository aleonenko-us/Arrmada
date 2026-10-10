import type { ConnectionItem } from "../../lib/connectionsApi";
import { connectionDot } from "../../lib/connectionStatus";

// StatusDot is a connection's health at a glance: green working, amber failing, red paused
// after repeated failures, grey not set up, never checked or switched off.
export function StatusDot({ item }: { item: Pick<ConnectionItem, "state" | "configured"> }) {
  const d = connectionDot(item);
  return (
    <span
      role="img"
      aria-label={d.label}
      title={d.label}
      className="inline-block h-2.5 w-2.5 flex-none rounded-full"
      style={{ background: d.color }}
    />
  );
}
