import { forwardRef, type SelectHTMLAttributes } from "react";
import { CONTROL, CONTROL_DEFAULT } from "./Input";

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function Select(
  { className = CONTROL_DEFAULT, ...rest },
  ref,
) {
  return <select ref={ref} className={`${CONTROL} ${className}`} {...rest} />;
});
