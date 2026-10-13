import { Children, cloneElement, isValidElement, useId, type ReactElement, type ReactNode } from "react";

export interface FieldProps {
  label: ReactNode;
  /** Help under the control; read out after the label (aria-describedby). */
  hint?: ReactNode;
  /** A problem with the value: shown in red and read out with the field. */
  error?: ReactNode;
  /**
   * The control is not the only child (a select with its save state beside it): pass the
   * control's own id here and Field labels that, leaving the children as they are.
   */
  controlId?: string;
  className?: string;
  children: ReactNode;
}

// The small mono eyebrow every form uses above a control.
export const FIELD_LABEL = "font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint";

// Field is a label, a control and its help, wired together: the label points at the
// control (clicking it focuses the control), and the hint and error are the control's
// description. With one child it gives that child an id (unless it has one) and
// aria-describedby; with controlId it only labels the control that carries that id.
export function Field({ label, hint, error, controlId, className = "", children }: FieldProps) {
  const auto = useId();
  const hintId = `${auto}-hint`;
  const errorId = `${auto}-error`;
  const describedBy = [hint ? hintId : "", error ? errorId : ""].filter(Boolean).join(" ") || undefined;

  let id = controlId;
  let body: ReactNode = children;
  if (!controlId) {
    const only = Children.only(children);
    if (!isValidElement(only)) throw new Error("Field: pass one control, or controlId");
    const el = only as ReactElement<{ id?: string; "aria-describedby"?: string; "aria-invalid"?: boolean }>;
    id = el.props.id ?? auto;
    body = cloneElement(el, {
      id,
      "aria-describedby": [el.props["aria-describedby"], describedBy].filter(Boolean).join(" ") || undefined,
      "aria-invalid": error ? true : el.props["aria-invalid"],
    });
  }
  return (
    <div className={`flex flex-col gap-1.5 ${className}`}>
      <label htmlFor={id} className={FIELD_LABEL}>{label}</label>
      {body}
      {hint && <span id={hintId} className="text-[10.5px] text-ink-faint">{hint}</span>}
      {error && <span id={errorId} role="alert" className="text-[11px]" style={{ color: "var(--reject-text)" }}>{error}</span>}
    </div>
  );
}
