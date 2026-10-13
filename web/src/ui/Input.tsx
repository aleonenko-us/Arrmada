import { forwardRef, type InputHTMLAttributes } from "react";

// The form control look every page uses: the raised panel colour, a hairline border and
// ink text. className replaces the default width, padding and text size (pass e.g.
// "w-24 px-3 py-2 font-mono text-base" for a narrow mono box).
export const CONTROL = "rounded-lg border border-line bg-panel-2 text-ink";
export const CONTROL_DEFAULT = "w-full px-3 py-2 text-md";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className = CONTROL_DEFAULT, ...rest },
  ref,
) {
  return <input ref={ref} className={`${CONTROL} ${className}`} {...rest} />;
});
