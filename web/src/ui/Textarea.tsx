import { forwardRef, type TextareaHTMLAttributes } from "react";
import { CONTROL, CONTROL_DEFAULT } from "./Input";

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
  { className = CONTROL_DEFAULT, ...rest },
  ref,
) {
  return <textarea ref={ref} className={`${CONTROL} ${className}`} {...rest} />;
});
