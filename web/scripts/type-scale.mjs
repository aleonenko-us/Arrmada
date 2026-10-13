// FE-30 codemod: rewrites arbitrary font sizes (text-[11.5px]) to the named type scale in
// tailwind.config.js and prints what it changed, per file. Run once; afterwards the eslint
// rule (no text-[Npx]) keeps new ones out.
//
//   node scripts/type-scale.mjs          rewrite src/ and report
//   node scripts/type-scale.mjs --dry    report only
//
// The floor: text that carries information is at least 11px (xs). 8 to 10.5px become 11px,
// except an uppercase mono eyebrow label (both classes in the same string), which becomes
// 2xs (10.5px): its tracking and capitals keep it legible at that size.
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

// Every size in use, mapped to its token. Ties go up for body sizes (11.5 → 12, 13 → 13.5)
// so small text grows rather than shrinks, and down for the one dialog-title size (16 →
// 15, the size the other dialog titles already use).
const MAP = {
  8: "xs", 8.5: "xs", 9: "xs", 9.5: "xs", 10: "xs", 10.5: "xs",
  11: "xs", 11.5: "sm", 12: "sm", 12.5: "base", 13: "md", 13.5: "md", 14: "md",
  15: "lg", 16: "lg", 17: "xl", 18: "xl", 19: "2xl", 20: "2xl", 21: "2xl",
  22: "3xl", 24: "3xl", 26: "3xl", 30: "display", 34: "display-lg", 38: "display-lg",
};
const EYEBROW_MAX = 10.5;

const dry = process.argv.includes("--dry");
const root = new URL("../src/", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");

function walk(dir, out = []) {
  for (const f of readdirSync(dir)) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(tsx?|jsx?)$/.test(f)) out.push(p);
  }
  return out;
}

// The class string a match sits in: from the quote before it to the quote after it.
function classString(src, at) {
  const quotes = /["'`]/g;
  let start = 0;
  for (let i = at; i >= 0; i--) if (`"'\``.includes(src[i])) { start = i + 1; break; }
  quotes.lastIndex = at;
  const end = quotes.exec(src)?.index ?? src.length;
  return src.slice(start, end);
}

const RE = /((?:[a-z0-9-]+:)*)text-\[(\d+(?:\.\d+)?)px\]/g;
let total = 0;
const unknown = new Set();
for (const file of walk(root)) {
  const src = readFileSync(file, "utf8");
  const counts = {};
  const out = src.replace(RE, (whole, prefix, num, offset) => {
    const px = Number(num);
    let token = MAP[px];
    if (!token) { unknown.add(`${px}px`); return whole; }
    if (px <= EYEBROW_MAX) {
      const cls = classString(src, offset).split(/\s+/);
      if (cls.includes("uppercase") && cls.includes("font-mono")) token = "2xs";
    }
    const key = `${px}px→${token}`;
    counts[key] = (counts[key] ?? 0) + 1;
    return `${prefix}text-${token}`;
  });
  const n = Object.values(counts).reduce((a, b) => a + b, 0);
  if (!n) continue;
  total += n;
  console.log(`${relative(root, file)}: ${n}  ${Object.entries(counts).map(([k, v]) => `${k}×${v}`).join(" ")}`);
  if (!dry) writeFileSync(file, out);
}
console.log(`${dry ? "would rewrite" : "rewrote"} ${total} sizes`);
if (unknown.size) {
  console.error(`no token for: ${[...unknown].join(", ")} — add them to MAP`);
  process.exit(1);
}
