import { useEffect, useRef } from "react";
import { useQuery } from "../../lib/query";
import { qualityApi } from "../../lib/qualityApi";
import { Button, Menu, StatusChip } from "../../ui";
import { requestProfiles, selectedProfile } from "./profilePick";

// StaffRequestButton is a Discover title's Request for staff (issue #50): a split button
// whose main part asks for the title on the profile it names ("＋ Request · HD-1080p") and
// whose chevron picks another. It's its own chunk, loaded only for staff, along with the
// profiles call (staff-only on the server): a requester's Discover never fetches either.
//
// value is the parent's pick; once the list is here it's settled to a listed profile (the
// default to start), so what the parent sends is always what the button says. Without a
// list (none, or it failed) it's a plain Request and the server uses its default.
export function StaffRequestButton({ media, label, value, onChange, onRequest, busy = false, disabled = false }: {
  media: "movie" | "series";
  label: string;
  value: string;
  onChange: (key: string) => void;
  onRequest: () => void;
  busy?: boolean;
  disabled?: boolean;
}) {
  const { data } = useQuery(`discover:request-profiles:${media}`, () => qualityApi.qualityProfiles(media).then((r) => requestProfiles(r.profiles, media)), { staleMs: 60_000 });
  const profiles = data ?? [];
  const sel = selectedProfile(profiles, value);
  const selKey = sel?.key;
  useEffect(() => { if (selKey && selKey !== value) onChange(selKey); }, [selKey, value, onChange]);

  // The title sheet is a dialog that closes on Escape, and it hears the key first. With the
  // list open, Escape should close only the list: catch it before the dialog does and
  // close the list from its own trigger.
  const groupRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      const trigger = groupRef.current?.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]');
      if (trigger?.getAttribute("aria-expanded") !== "true") return;
      e.stopPropagation();
      e.preventDefault();
      trigger.click();
      trigger.focus();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  if (!sel) {
    return <Button variant="primary" onClick={onRequest} busy={busy} busyLabel="Requesting…" disabled={disabled}>{label}</Button>;
  }
  return (
    <div ref={groupRef} role="group" aria-label="Request and quality profile" className="inline-flex max-w-full items-stretch">
      {/* A long profile name shortens on a narrow screen; the label never does. */}
      <Button variant="primary" onClick={onRequest} busy={busy} busyLabel="Requesting…" disabled={disabled} title={`${label} · ${sel.name}`}
        className="inline-flex min-w-0 items-center rounded-r-none px-3 sm:px-4">
        <span className="flex-none">{label}</span>
        <span aria-hidden className="mx-1 flex-none opacity-70 sm:mx-1.5">·</span>
        <span className="sr-only"> with </span>
        <span className="min-w-0 max-w-[12rem] truncate">{sel.name}</span>
      </Button>
      <Menu
        label={`Quality profile: ${sel.name}`}
        header={<span className="font-mono text-2xs uppercase tracking-wide text-ink-faint">Quality profile</span>}
        trigger={<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="m6 9 6 6 6-6" /></svg>}
        triggerClassName="bg-accent-grad text-accent-ink grid h-full min-h-[36px] place-items-center rounded-r-lg px-2.5"
        triggerStyle={{ borderLeft: "1px solid rgba(0,0,0,.22)" }}
        align="right"
        menuWidthClassName="w-64 max-w-[calc(100vw-2rem)]"
        itemClassName="min-h-[40px]"
        items={profiles.map((p) => {
          const on = p.key === sel.key;
          return {
            label: (
              <span className="flex items-center gap-2">
                <span aria-hidden className="w-4 flex-none text-center" style={{ color: "var(--accent-text)" }}>{on ? "✓" : ""}</span>
                <span className="min-w-0 flex-1 truncate" title={p.name}>{p.name}</span>
                {p.is_default && <StatusChip tone="faint" size="xs" className="flex-none">Default</StatusChip>}
                {on && <span className="sr-only"> (selected)</span>}
              </span>
            ),
            onSelect: () => onChange(p.key),
          };
        })}
      />
    </div>
  );
}
