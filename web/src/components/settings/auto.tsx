import { useId } from "react";
import type { AutoSave } from "../../lib/useAutoSave";
import { Field } from "../../ui/Field";
import { Toggle, input, inputStyle } from "./ui";

// The auto-saving controls every Settings card is built from. Each takes an AutoSave
// (lib/useAutoSave for any endpoint, or useSetting(key) for /settings) and shows its own
// state next to it, so there is no Save button and no page-wide "unsaved changes":
//
//   AutoToggle  saves when flipped; flips back if the save fails.
//   AutoSelect  saves when a choice is picked.
//   AutoText    saves on blur or Enter when changed; Esc puts the saved value back.
//   AutoNumber  AutoText for whole numbers, checked against min/max before sending.
//   SavedTick   "Saving…", then "Saved ✓" for two seconds, or the error with Retry.
//
// Credentials and folder paths are not auto-saved: they keep an explicit, validated action.

type Saveish = Pick<AutoSave<unknown>, "status" | "error" | "canRetry" | "retry">;

// SavedTick is the inline save state for one control (or a group that saves together).
export function SavedTick({ save, className = "" }: { save: Saveish; className?: string }) {
  const base = `text-xs ${className}`;
  return (
    <span aria-live="polite" className="min-h-[1em]">
      {save.status === "saving" && <span className={`${base} text-ink-faint`}>Saving…</span>}
      {save.status === "saved" && <span className={base} style={{ color: "var(--good)" }}>Saved ✓</span>}
      {save.status === "error" && (
        <span role="alert" className={base} style={{ color: "var(--reject-text, var(--reject))" }}>
          {save.error}
          {save.canRetry && (
            <>
              {" "}
              <button type="button" onClick={save.retry} className="font-semibold underline" style={{ color: "var(--accent)" }}>Retry</button>
            </>
          )}
        </span>
      )}
    </span>
  );
}

export function AutoToggle({ label, hint, save, disabled }: { label: string; hint: string; save: AutoSave<boolean>; disabled?: boolean }) {
  return (
    <div className="flex flex-col gap-1">
      <Toggle label={label} hint={hint} checked={save.value} disabled={disabled}onChange={(v) => void save.commit(v)} />
      {save.status !== "idle" && <div className="flex justify-end"><SavedTick save={save} /></div>}
    </div>
  );
}

export function AutoSelect<T extends string>({ label, save, options, className = "w-full max-w-[280px]", children }: {
  label: string;
  save: AutoSave<T>;
  options: readonly { value: T; label: string }[];
  className?: string;
  children?: React.ReactNode;
}) {
  const id = useId();
  return (
    <Field label={label} controlId={id}>
      <span className="flex flex-wrap items-center gap-2">
        <select id={id} aria-describedby={children ? `${id}-help` : undefined} value={save.value} onChange={(e) => void save.commit(e.target.value as T)} className={`rounded-lg px-3 py-2 text-base ${className}`} style={inputStyle}>
          {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
        <SavedTick save={save} />
      </span>
      <Help id={`${id}-help`}>{children}</Help>
    </Field>
  );
}

// Help is the text under an auto-saving control, read out as its description. `contents`
// keeps each piece a direct row of the field, as it was before it was wrapped.
function Help({ id, children }: { id: string; children?: React.ReactNode }) {
  return children ? <div id={id} className="contents">{children}</div> : null;
}

// textKeys is the keyboard half of blur-to-save: Enter commits, Esc reverts.
function textKeys(save: AutoSave<string>) {
  return (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") { e.preventDefault(); void save.commit(); }
    if (e.key === "Escape" && save.dirty) { e.preventDefault(); save.revert(); }
  };
}

export function AutoText({ label, save, placeholder, mono = true, className, inputMode, maxLength, filter, children }: {
  label: string;
  save: AutoSave<string>;
  placeholder?: string;
  mono?: boolean;
  className?: string;
  inputMode?: React.HTMLAttributes<HTMLInputElement>["inputMode"];
  maxLength?: number;
  /** Cleans each keystroke (digits only, upper case) before it's shown. */
  filter?: (v: string) => string;
  /** Help and previews under the field. */
  children?: React.ReactNode;
}) {
  const id = useId();
  return (
    <Field label={label} controlId={id}>
      <input
        id={id}
        aria-describedby={children ? `${id}-help` : undefined}
        value={save.value}
        onChange={(e) => save.set(filter ? filter(e.target.value) : e.target.value)}
        onBlur={() => void save.commit()}
        onKeyDown={textKeys(save)}
        placeholder={placeholder}
        inputMode={inputMode}
        maxLength={maxLength}
        aria-invalid={save.status === "error" || undefined}
        className={className ?? (mono ? input : input.replace("font-mono ", ""))}
        style={save.status === "error" ? { ...inputStyle, border: "1px solid var(--reject)" } : inputStyle}
      />
      <Help id={`${id}-help`}>{children}</Help>
      <SavedTick save={save} />
    </Field>
  );
}

// AutoNumber edits a whole number as text so a half-typed "" or "-" is never coerced to 0
// on the way through; the number is checked against min/max only when it's committed.
export function AutoNumber({ label, save, min, max, placeholder, className, children }: {
  label: string;
  save: AutoSave<string>;
  min: number;
  max: number;
  placeholder?: string;
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <AutoText label={label} save={save} placeholder={placeholder} mono={false} inputMode="numeric" className={className} filter={(v) => v.replace(/[^0-9]/g, "").slice(0, String(max).length)}>
      {children}
      <span className="sr-only">{`Between ${min} and ${max}.`}</span>
    </AutoText>
  );
}

// wholeNumber is the validate for an AutoNumber: a message when v isn't a whole number in
// [min, max], or null.
export function wholeNumber(min: number, max: number, what = "This") {
  return (v: string): string | null => {
    const t = v.trim();
    if (t === "" || !/^\d+$/.test(t)) return `${what} needs a number from ${min} to ${max}.`;
    const n = Number(t);
    return n < min || n > max ? `${what} must be between ${min} and ${max}.` : null;
  };
}
