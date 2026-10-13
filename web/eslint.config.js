// ESLint 9 flat config for the web UI.
//
// `npm run lint` must pass with 0 errors. Errors are real bugs (rules-of-hooks), the
// UI kit's bans (FE-14): native dialogs, hand-built overlays and page-local toasts, and
// accessibility (FE-15): every jsx-a11y recommended rule, plus a name on every control.
import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import jsxA11y from "eslint-plugin-jsx-a11y";
import globals from "globals";
import { defineConfig } from "eslint/config";

const ctrlLabel = jsxA11y.flatConfigs.recommended.rules["jsx-a11y/control-has-associated-label"][1];

// Page-local toasts: a `flash` helper or a [toast, setToast] state with its own timer.
// useToast() from src/ui is the one live region (stacking, timers, the error tone).
const NO_LOCAL_TOAST = [
  {
    selector: "VariableDeclarator[id.name='flash']",
    message: "Page-local toast helper: call useToast() from src/ui (name it toast or say, not flash).",
  },
  {
    selector: "VariableDeclarator[init.callee.name='useState'][id.elements.0.name=/^toast/i]",
    message: "Page-local toast state: use useToast() from src/ui instead of a [toast, setToast] state and a timer.",
  },
];
// Hand-built overlays: the UI kit's Modal (variant dialog or sheet), ConfirmDialog or
// Scrim owns these now.
// Arbitrary font sizes: every size is a token of the type scale in tailwind.config.js
// (FE-30: text-2xs … text-display-lg, 11px floor for text that carries information).
const ARBITRARY_TEXT = /text-\[[0-9.]+px\]/;
const NO_ARBITRARY_TEXT_SIZE = [
  {
    selector: `Literal[value=${ARBITRARY_TEXT}]`,
    message: "Arbitrary font size: use the type scale (text-2xs, text-xs, text-sm, text-base, text-md, text-lg … text-display-lg).",
  },
  {
    selector: `TemplateElement[value.raw=${ARBITRARY_TEXT}]`,
    message: "Arbitrary font size: use the type scale (text-2xs, text-xs, text-sm, text-base, text-md, text-lg … text-display-lg).",
  },
];
const NO_HAND_BUILT_OVERLAY = [
  {
    selector: "Literal[value=/fixed inset-0/]",
    message: "Hand-built overlay: use <Modal> (variant dialog or sheet), <ConfirmDialog> or <Scrim> from src/ui.",
  },
  {
    selector: "TemplateElement[value.raw=/fixed inset-0/]",
    message: "Hand-built overlay: use <Modal> (variant dialog or sheet), <ConfirmDialog> or <Scrim> from src/ui.",
  },
];

export default defineConfig(
  { ignores: ["dist/**", "node_modules/**"] },
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: "module",
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "jsx-a11y": jsxA11y,
    },
    rules: {
      // Hooks called conditionally or out of order are real bugs: error.
      "react-hooks/rules-of-hooks": "error",
      // Warn only: many existing omissions are deliberate (run-once / run-on-change effects).
      "react-hooks/exhaustive-deps": "warn",

      // jsx-a11y recommended, all at error (FE-15): a field without a label, a control with
      // no name or a click-only div fails the build, not just a screen reader.
      ...jsxA11y.flatConfigs.recommended.rules,
      // Labels here often hold their text a couple of spans deep (a bold name over a faint
      // hint); depth 3 lets the rule see it.
      "jsx-a11y/label-has-associated-control": ["error", { depth: 3 }],
      // Off in recommended: every button, link and option must have a name. Icon-only
      // buttons use <IconButton label=…>; an empty header cell gets sr-only text.
      "jsx-a11y/control-has-associated-label": [
        "error",
        { ...ctrlLabel, depth: 4 },
      ],

      // Native dialogs can't be styled, block the tab and some browsers suppress them:
      // the UI kit's Confirm/Toast replace them everywhere (FE-14).
      "no-restricted-globals": [
        "error",
        { name: "confirm", message: "Use useConfirm() from src/ui instead of a native dialog." },
        { name: "alert", message: "Use useToast() from src/ui instead of a native alert." },
        { name: "prompt", message: "Use a <Modal> with a field from src/ui instead of a native prompt." },
      ],
      "no-restricted-properties": [
        "error",
        { object: "window", property: "confirm", message: "Use useConfirm() from src/ui instead of a native dialog." },
        { object: "window", property: "alert", message: "Use useToast() from src/ui instead of a native alert." },
        { object: "window", property: "prompt", message: "Use a <Modal> with a field from src/ui instead of a native prompt." },
      ],
      "no-restricted-syntax": ["error", ...NO_LOCAL_TOAST, ...NO_ARBITRARY_TEXT_SIZE],

      // `set.has(k) ? set.delete(k) : set.add(k)` toggles are an established idiom here.
      "@typescript-eslint/no-unused-expressions": ["error", { allowTernary: true }],
    },
  },
  {
    // Outside the kit itself, no overlay is hand-built. (A later block's no-restricted-syntax
    // replaces the earlier one, so the toast bans are repeated here.)
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/ui/**"],
    rules: {
      "no-restricted-syntax": ["error", ...NO_LOCAL_TOAST, ...NO_ARBITRARY_TEXT_SIZE, ...NO_HAND_BUILT_OVERLAY],
    },
  },
);
