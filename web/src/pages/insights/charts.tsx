import { useEffect, useRef, useState, type PointerEvent, type ReactNode, type RefObject } from "react";
import { Link } from "react-router-dom";

// Insights' charts, drawn as SVG at the width they really have (measured), so lines,
// bars and text keep their proportions on a phone and on a wide screen alike. Every
// colour comes from the theme tokens; text wears the ink tokens, never a series colour,
// and series that share hues the owner's palette can't separate for every reader also
// differ in dash pattern and carry a label at the line's end.

export interface Series { label: string; color: string; values: number[]; dash?: string }

// useWidth measures an element's content width and follows it as it resizes.
function useWidth<T extends HTMLElement>(): [RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setW(Math.floor(el.getBoundingClientRect().width));
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}

// niceMax rounds a maximum up to 1, 2, 2.5 or 5 × 10^k, so the axis ticks are round.
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

const TICK_FONT = 11; // px — the type scale's xs
const ink = { faint: "var(--ink-faint)", dim: "var(--ink-dim)" };

// The tooltip box, positioned inside the chart's relative wrapper.
function Tip({ x, width, title, rows }: { x: number; width: number; title: string; rows: { label: string; color: string; value: string; dash?: string }[] }) {
  const boxW = 168;
  const left = Math.min(Math.max(0, x + 12), Math.max(0, width - boxW));
  const flip = x + 12 + boxW > width;
  return (
    <div className="pointer-events-none absolute top-1 z-10 rounded-lg px-2.5 py-1.5 text-xs shadow"
      style={{ left: flip ? Math.max(0, x - boxW - 12) : left, width: boxW, background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
      <div className="mb-0.5 font-mono text-2xs text-ink-faint">{title}</div>
      {rows.map((r) => (
        <div key={r.label} className="flex items-center gap-1.5">
          <svg width="14" height="6" aria-hidden className="flex-none"><line x1="0" x2="14" y1="3" y2="3" stroke={r.color} strokeWidth={2} strokeDasharray={r.dash} /></svg>
          <span className="font-mono font-semibold tabular-nums">{r.value}</span>
          <span className="truncate text-ink-dim">{r.label}</span>
        </div>
      ))}
    </div>
  );
}

// LineChart draws one or more series over shared x positions. xLabels are the axis
// labels (blank = no label at that point; crowded labels are thinned), tipTitles what
// the tooltip calls each point (falls back to xLabels). fmt formats values.
export function LineChart({ series, xLabels, tipTitles, fmt = (v) => String(Math.round(v)), height = 200, caption }: {
  series: Series[]; xLabels: string[]; tipTitles?: string[]; fmt?: (v: number) => string; height?: number; caption: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const n = Math.max(1, xLabels.length);
  const max = niceMax(Math.max(0, ...series.flatMap((s) => s.values)));
  const ticks = [0, max / 2, max];
  const tickText = ticks.map(fmt);
  const padL = 8 + Math.max(...tickText.map((t) => t.length)) * 6.6;
  const endLabels = series.length > 1 && series.length <= 4;
  const padR = endLabels ? 8 + Math.max(...series.map((s) => s.label.length)) * 6.6 : 10;
  const padT = 8, padB = 22;
  const W = Math.max(width, 1);
  const plotW = Math.max(1, W - padL - padR);
  const x = (i: number) => padL + (n <= 1 ? plotW / 2 : (i / (n - 1)) * plotW);
  const y = (v: number) => padT + (1 - v / max) * (height - padT - padB);

  // Thin the axis labels so none collide: keep one only when it sits a label's width
  // past the last one drawn.
  const shown: number[] = [];
  let lastX = -Infinity;
  xLabels.forEach((l, i) => {
    if (!l) return;
    const gap = Math.max(48, l.length * 6.6 + 12);
    if (x(i) - lastX >= gap && x(i) + (l.length * 3.3) <= W) { shown.push(i); lastX = x(i); }
  });

  // End labels would overlap when two lines finish close together: nudge them apart.
  const ends = series.map((s, si) => ({ si, y: y(s.values[s.values.length - 1] ?? 0) })).sort((a, b) => a.y - b.y);
  for (let i = 1; i < ends.length; i++) if (ends[i].y - ends[i - 1].y < 12) ends[i].y = ends[i - 1].y + 12;

  const onMove = (e: PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - r.left;
    const i = n <= 1 ? 0 : Math.round(((px - padL) / plotW) * (n - 1));
    setHover(Math.min(n - 1, Math.max(0, i)));
  };

  return (
    <div ref={ref} className="relative">
      {width > 0 && (
        <svg width={W} height={height} role="img" aria-label={caption} onPointerMove={onMove} onPointerLeave={() => setHover(null)} style={{ display: "block", touchAction: "pan-y" }}>
          {ticks.map((t, i) => (
            <g key={i}>
              <line x1={padL} x2={W - padR} y1={y(t)} y2={y(t)} stroke="var(--line-soft)" strokeWidth={1} />
              <text x={padL - 6} y={y(t) + 4} textAnchor="end" fontSize={TICK_FONT} fill={ink.faint}>{tickText[i]}</text>
            </g>
          ))}
          {shown.map((i) => <text key={i} x={x(i)} y={height - 6} textAnchor="middle" fontSize={TICK_FONT} fill={ink.faint}>{xLabels[i]}</text>)}
          {series.map((s) => (
            <polyline key={s.label} fill="none" stroke={s.color} strokeWidth={2} strokeDasharray={s.dash} strokeLinejoin="round" strokeLinecap="round"
              points={s.values.map((v, i) => `${x(i)},${y(v)}`).join(" ")} />
          ))}
          {endLabels && ends.map((e) => (
            <text key={e.si} x={W - padR + 6} y={e.y + 4} fontSize={TICK_FONT} fill={ink.dim}>{series[e.si].label}</text>
          ))}
          {hover !== null && (
            <g pointerEvents="none">
              <line x1={x(hover)} x2={x(hover)} y1={padT} y2={height - padB} stroke="var(--ink-faint)" strokeWidth={1} />
              {series.map((s) => <circle key={s.label} cx={x(hover)} cy={y(s.values[hover] ?? 0)} r={4} fill={s.color} stroke="var(--panel)" strokeWidth={2} />)}
            </g>
          )}
        </svg>
      )}
      {hover !== null && width > 0 && (
        <Tip x={x(hover)} width={W} title={(tipTitles ?? xLabels)[hover] || xLabels[hover] || ""}
          rows={series.map((s) => ({ label: s.label, color: s.color, dash: s.dash, value: fmt(s.values[hover] ?? 0) }))} />
      )}
      <DataTable caption={caption} heads={series.map((s) => s.label)} rows={(tipTitles ?? xLabels).map((t, i) => [t || xLabels[i] || String(i + 1), ...series.map((s) => fmt(s.values[i] ?? 0))])} />
    </div>
  );
}

// BarChart is a column per bucket (day of week, hour of day); each bar carries its own
// hover tooltip.
export function BarChart({ values, labels, tipTitles, fmt = (v) => String(Math.round(v)), height = 180, caption }: {
  values: number[]; labels: string[]; tipTitles?: string[]; fmt?: (v: number) => string; height?: number; caption: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const max = niceMax(Math.max(0, ...values));
  const padT = 6, padB = 20, padL = 4, padR = 4;
  const W = Math.max(width, 1);
  const bw = (W - padL - padR) / Math.max(1, values.length);
  const y = (v: number) => padT + (1 - v / max) * (height - padT - padB);
  const base = height - padB;
  return (
    <div ref={ref} className="relative">
      {width > 0 && (
        <svg width={W} height={height} role="img" aria-label={caption} onPointerLeave={() => setHover(null)} style={{ display: "block" }}>
          <line x1={padL} x2={W - padR} y1={base} y2={base} stroke="var(--line)" strokeWidth={1} />
          {values.map((v, i) => {
            const h = Math.max(0, base - y(v));
            const bx = padL + i * bw + 1, bWidth = Math.max(1, bw - 2); // a 2px gap between bars
            const r = Math.min(4, bWidth / 2, h);
            return (
              <g key={i} onPointerEnter={() => setHover(i)}>
                {/* Hit area: the whole column, not just the painted bar. */}
                <rect x={padL + i * bw} y={padT} width={bw} height={base - padT} fill="transparent" />
                {h > 0 && <path d={`M${bx},${base} V${base - h + r} Q${bx},${base - h} ${bx + r},${base - h} H${bx + bWidth - r} Q${bx + bWidth},${base - h} ${bx + bWidth},${base - h + r} V${base} Z`}
                  fill="var(--accent)" opacity={hover === null || hover === i ? 1 : 0.55} />}
              </g>
            );
          })}
          {labels.map((l, i) => (l ? <text key={i} x={padL + i * bw + bw / 2} y={height - 5} textAnchor="middle" fontSize={TICK_FONT} fill={ink.faint}>{l}</text> : null))}
        </svg>
      )}
      {hover !== null && width > 0 && (
        <Tip x={padL + hover * bw + bw / 2} width={W} title={(tipTitles ?? labels)[hover] || labels[hover] || ""} rows={[{ label: caption, color: "var(--accent)", value: fmt(values[hover] ?? 0) }]} />
      )}
      <DataTable caption={caption} heads={["Value"]} rows={values.map((v, i) => [(tipTitles ?? labels)[i] || labels[i] || String(i), fmt(v)])} />
    </div>
  );
}

// HBarChart ranks named rows; a row with a link opens it (a person's People page).
export function HBarChart({ rows, fmt = (v) => String(Math.round(v)) }: { rows: { label: string; value: number; to?: string }[]; fmt?: (v: number) => string }) {
  if (rows.length === 0) return <div className="py-6 text-center text-xs text-ink-faint">No data yet.</div>;
  const max = Math.max(1e-9, ...rows.map((r) => r.value));
  return (
    <div className="flex flex-col gap-1.5">
      {rows.map((r, i) => (
        <div key={i} className="flex items-center gap-2 text-xs">
          {r.to
            ? <Link to={r.to} className="w-[92px] flex-none truncate text-ink-dim hover:text-[var(--ink)] hover:underline" title={r.label}>{r.label || "—"}</Link>
            : <span className="w-[92px] flex-none truncate text-ink-dim" title={r.label}>{r.label || "—"}</span>}
          <span className="h-3.5 flex-1 overflow-hidden rounded" style={{ background: "var(--panel-2)" }}>
            <span className="block h-full rounded" style={{ width: `${(r.value / max) * 100}%`, background: "var(--accent)" }} />
          </span>
          <span className="w-10 flex-none text-right font-mono text-ink-faint">{fmt(r.value)}</span>
        </div>
      ))}
    </div>
  );
}

// DataTable is each chart's numbers for screen readers: the tooltip enhances, it never
// gates a value. (The wrapper is what's visually hidden: a table ignores a 1px width and
// would widen the page.)
function DataTable({ caption, heads, rows }: { caption: string; heads: string[]; rows: string[][] }) {
  return (
    <div className="sr-only">
      <table aria-label={caption}>
        <thead><tr><th scope="col">Point</th>{heads.map((h) => <th key={h} scope="col">{h}</th>)}</tr></thead>
        <tbody>{rows.map((r, i) => <tr key={i}>{r.map((c, j) => (j === 0 ? <th key={j} scope="row">{c}</th> : <td key={j}>{c}</td>))}</tr>)}</tbody>
      </table>
    </div>
  );
}

// Legend keys a line series with a short stroke (with its dash), not a box.
export function Legend({ items }: { items: { label: string; color: string; dash?: string }[] }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-3 text-xs">
      {items.map((it) => (
        <span key={it.label} className="inline-flex items-center gap-1 text-ink-dim">
          <svg width="16" height="6" aria-hidden><line x1="1" x2="15" y1="3" y2="3" stroke={it.color} strokeWidth={2} strokeDasharray={it.dash} strokeLinecap="round" /></svg>
          {it.label}
        </span>
      ))}
    </span>
  );
}

export function ChartCard({ title, extra, children }: { title: string; extra?: ReactNode; children: ReactNode }) {
  return (
    <div className="min-w-0 rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="text-base font-bold">{title}</div>
        {extra}
      </div>
      {children}
    </div>
  );
}
