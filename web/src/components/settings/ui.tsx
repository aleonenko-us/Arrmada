// The building blocks every Settings section is made of, shared so the sections (one file
// each under pages/settings/) look the same. The auto-saving controls (AutoToggle,
// AutoText, AutoNumber, AutoSelect, SavedTick) are in ./auto.tsx, built on
// lib/useAutoSave (any endpoint) and lib/useSettings useSetting (the /settings keys). A
// labelled control is ui/Field.
import { useEffect, useId } from "react";
import { useLocation } from "react-router-dom";
import { Switch } from "../../ui/Switch";

// useScrollWhenLoaded scrolls a card that fills in after its own fetch back into view when
// the address names it (/settings/convert#…): the hub's own scroll ran before the card,
// or the ones above it, had their full height.
export function useScrollWhenLoaded(id: string, loaded: boolean) {
  const { hash } = useLocation();
  useEffect(() => {
    if (loaded && hash === `#${id}`) document.getElementById(id)?.scrollIntoView({ block: "start" });
  }, [loaded, hash, id]);
}

export const input = "w-full rounded-lg px-3 py-2 font-mono text-base";
export const inputStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

// id makes a section a deep-link target (LINKS.apiKeys and friends); scroll-mt keeps the
// sticky page header from covering its title when a link scrolls to it.
export function Section({ id, title, subtitle, children }: { id?: string; title: string; subtitle: React.ReactNode; children: React.ReactNode }) {
  return (
    <div id={id} className="scroll-mt-20 rounded-xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <h2 className="m-0 text-md font-bold">{title}</h2>
      <p className="mb-4 mt-0.5 text-sm text-ink-faint">{subtitle}</p>
      <div className="flex flex-col gap-4">{children}</div>
    </div>
  );
}

export function Preview({ children }: { children: React.ReactNode }) {
  return <span className="text-xs text-ink-dim">→ <span className="font-mono" style={{ color: "var(--accent)" }}>{children}</span></span>;
}

export function TokenList({ tokens }: { tokens: string[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {tokens.map((t) => (
        <code key={t} className="rounded px-1.5 py-0.5 font-mono text-xs" style={{ background: "var(--panel-2)", color: "var(--ink-dim)" }}>{`{${t}}`}</code>
      ))}
    </div>
  );
}

export function Note({ tone, children }: { tone: "warn" | "info"; children: React.ReactNode }) {
  const color = tone === "warn" ? "var(--avoid)" : "var(--line)";
  return (
    <div
      className="rounded-lg p-3 text-sm leading-relaxed"
      style={{ background: "var(--panel-2)", border: `1px solid ${color}`, color: "var(--ink-dim)" }}
    >
      {children}
    </div>
  );
}

// Toggle is a Settings on/off row: the name and its hint on the left, the switch on the
// right. The name is the switch's <label> (clicking it flips the switch too) and the hint
// its description.
export function Toggle({ label, hint, checked, onChange, disabled }: { label: string; hint: string; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  const id = useId();
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="min-w-0">
        <label htmlFor={id} className="block text-base font-semibold">{label}</label>
        <div id={`${id}-hint`} className="text-xs text-ink-faint">{hint}</div>
      </div>
      <Switch id={id} checked={checked} onChange={onChange} disabled={disabled} aria-describedby={`${id}-hint`} />
    </div>
  );
}
