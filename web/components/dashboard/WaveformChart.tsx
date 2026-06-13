"use client";

// WaveformChart — an interactive SVG scope for circuit SimResults. Renders one
// trace per signal column directly from circuit_simulate output (the static PNG
// from circuit_plot is for the MCP host model; humans get this). Toggle traces,
// hover for a cursor readout, log-x for AC Bode. Dependency-free (hand-rolled
// SVG), matching the dashboard's dark aesthetic.
import { useMemo, useRef, useState } from "react";

export type SimResult = {
  analysis: string;
  signals: string[];
  samples: number[][];
  engine?: string;
};

const PALETTE = ["#56b4ff", "#78dc82", "#ffaa46", "#eb6e96", "#be8cff", "#6edcdc", "#f4d35e", "#ef798a"];

export default function WaveformChart({ result, height = 300 }: { result: SimResult; height?: number }) {
  const { signals, samples, analysis } = result;
  const logX = analysis === "ac";

  // trace columns (skip the x column; hide AC phase by default)
  const allCols = useMemo(
    () => signals.map((name, i) => ({ name, i })).filter((c) => c.i > 0 && !(logX && c.name.endsWith("deg"))),
    [signals, logX],
  );
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [hoverX, setHoverX] = useState<number | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const W = 760;
  const H = height;
  const padL = 56;
  const padR = 14;
  const padT = 12;
  const padB = 30;
  const plotW = W - padL - padR;
  const plotH = H - padT - padB;

  const visible = allCols.filter((c) => !hidden.has(c.name));

  const { xmin, xmax, ymin, ymax } = useMemo(() => {
    let xmin = Infinity,
      xmax = -Infinity,
      ymin = Infinity,
      ymax = -Infinity;
    for (const row of samples) {
      let x = row[0];
      if (logX) {
        if (x <= 0) continue;
        x = Math.log10(x);
      }
      xmin = Math.min(xmin, x);
      xmax = Math.max(xmax, x);
      for (const c of visible) {
        const v = row[c.i];
        if (!Number.isFinite(v)) continue;
        ymin = Math.min(ymin, v);
        ymax = Math.max(ymax, v);
      }
    }
    if (!Number.isFinite(xmin)) {
      xmin = 0;
      xmax = 1;
    }
    if (xmax <= xmin) xmax = xmin + 1;
    if (!Number.isFinite(ymin) || ymax <= ymin) {
      ymin = (ymin || 0) - 1;
      ymax = (ymax || 0) + 1;
    }
    const yr = ymax - ymin;
    return { xmin, xmax, ymin: ymin - yr * 0.08, ymax: ymax + yr * 0.08 };
  }, [samples, visible, logX]);

  const sx = (x: number) => {
    let xv = x;
    if (logX && x > 0) xv = Math.log10(x);
    return padL + (plotW * (xv - xmin)) / (xmax - xmin);
  };
  const sy = (y: number) => padT + plotH - (plotH * (y - ymin)) / (ymax - ymin);

  const paths = useMemo(
    () =>
      visible.map((c) => {
        let d = "";
        let pen = false;
        for (const row of samples) {
          const v = row[c.i];
          if (!Number.isFinite(v)) {
            pen = false;
            continue;
          }
          const px = sx(row[0]).toFixed(1);
          const py = sy(v).toFixed(1);
          d += `${pen ? "L" : "M"}${px} ${py}`;
          pen = true;
        }
        return { name: c.name, d };
      }),
    [visible, samples, xmin, xmax, ymin, ymax],
  );

  const colorOf = (name: string) => PALETTE[allCols.findIndex((c) => c.name === name) % PALETTE.length];

  // x/y ticks
  const xTicks = useMemo(() => {
    const out: { x: number; label: string }[] = [];
    if (logX) {
      const lo = Math.ceil(xmin);
      const hi = Math.floor(xmax);
      for (let e = lo; e <= hi; e++) out.push({ x: padL + (plotW * (e - xmin)) / (xmax - xmin), label: engFmt(Math.pow(10, e)) });
    } else {
      for (let i = 0; i <= 5; i++) {
        const xv = xmin + ((xmax - xmin) * i) / 5;
        out.push({ x: padL + (plotW * i) / 5, label: engFmt(xv) });
      }
    }
    return out;
  }, [xmin, xmax, logX]);

  const yTicks = useMemo(() => {
    const out: { y: number; label: string }[] = [];
    for (let i = 0; i <= 4; i++) {
      const yv = ymin + ((ymax - ymin) * i) / 4;
      out.push({ y: padT + plotH - (plotH * i) / 4, label: trim(yv) });
    }
    return out;
  }, [ymin, ymax]);

  // hover → nearest sample
  const hoverRow = useMemo(() => {
    if (hoverX == null || samples.length === 0) return null;
    // map pixel back to data-x
    let best = 0;
    let bestD = Infinity;
    for (let r = 0; r < samples.length; r++) {
      const px = sx(samples[r][0]);
      const d = Math.abs(px - hoverX);
      if (d < bestD) {
        bestD = d;
        best = r;
      }
    }
    return samples[best];
  }, [hoverX, samples, xmin, xmax]);

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = svgRef.current?.getBoundingClientRect();
    if (!rect) return;
    const x = ((e.clientX - rect.left) / rect.width) * W;
    if (x >= padL && x <= padL + plotW) setHoverX(x);
    else setHoverX(null);
  };

  const xUnit = analysis === "tran" ? "time (s)" : analysis === "ac" ? "freq (Hz, log)" : analysis === "dc" ? "sweep" : "x";
  const yUnit = analysis === "ac" ? "dB" : "V";

  return (
    <div>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${W} ${H}`}
        className="w-full select-none"
        style={{ background: "#12141a", borderRadius: 10 }}
        onMouseMove={onMove}
        onMouseLeave={() => setHoverX(null)}
      >
        {/* grid + ticks */}
        {yTicks.map((t, i) => (
          <g key={`y${i}`}>
            <line x1={padL} y1={t.y} x2={padL + plotW} y2={t.y} stroke="#2c303a" strokeWidth={1} />
            <text x={padL - 6} y={t.y + 3} fontSize={10} fill="#7a808c" textAnchor="end">
              {t.label}
            </text>
          </g>
        ))}
        {xTicks.map((t, i) => (
          <g key={`x${i}`}>
            <line x1={t.x} y1={padT} x2={t.x} y2={padT + plotH} stroke="#2c303a" strokeWidth={1} />
            <text x={t.x} y={padT + plotH + 14} fontSize={10} fill="#7a808c" textAnchor="middle">
              {t.label}
            </text>
          </g>
        ))}
        {/* zero line */}
        {ymin < 0 && ymax > 0 && <line x1={padL} y1={sy(0)} x2={padL + plotW} y2={sy(0)} stroke="#4a4f5a" strokeWidth={1} />}
        {/* axes */}
        <line x1={padL} y1={padT} x2={padL} y2={padT + plotH} stroke="#78808c" strokeWidth={1} />
        <line x1={padL} y1={padT + plotH} x2={padL + plotW} y2={padT + plotH} stroke="#78808c" strokeWidth={1} />

        {/* traces */}
        {paths.map((p) => (
          <path key={p.name} d={p.d} fill="none" stroke={colorOf(p.name)} strokeWidth={1.8} />
        ))}

        {/* hover cursor */}
        {hoverRow && (
          <g>
            <line x1={sx(hoverRow[0])} y1={padT} x2={sx(hoverRow[0])} y2={padT + plotH} stroke="#ffffff55" strokeWidth={1} />
            {visible.map((c) => (
              <circle key={c.name} cx={sx(hoverRow[0])} cy={sy(hoverRow[c.i])} r={3} fill={colorOf(c.name)} />
            ))}
          </g>
        )}

        <text x={padL} y={H - 4} fontSize={10} fill="#5a606c">
          {xUnit}
        </text>
        <text x={6} y={padT + 8} fontSize={10} fill="#5a606c">
          {yUnit}
        </text>
      </svg>

      {/* legend / toggles + hover readout */}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        {allCols.map((c) => {
          const off = hidden.has(c.name);
          return (
            <button
              key={c.name}
              onClick={() =>
                setHidden((h) => {
                  const n = new Set(h);
                  n.has(c.name) ? n.delete(c.name) : n.add(c.name);
                  return n;
                })
              }
              className={`flex items-center gap-1.5 rounded-md px-2 py-1 text-xs ${off ? "opacity-35" : ""} bg-white/5 hover:bg-white/10`}
            >
              <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: colorOf(c.name) }} />
              <span className="font-mono">{c.name}</span>
              {hoverRow && !off && <span className="text-white/50">{trim(hoverRow[c.i])}</span>}
            </button>
          );
        })}
        {hoverRow && (
          <span className="ml-auto font-mono text-xs text-white/40">
            {xUnit.split(" ")[0]} = {engFmt(hoverRow[0])}
          </span>
        )}
      </div>
    </div>
  );
}

function engFmt(v: number): string {
  if (!Number.isFinite(v)) return "—";
  if (v === 0) return "0";
  const neg = v < 0;
  let a = Math.abs(v);
  const units: [number, string][] = [
    [1e12, "T"],
    [1e9, "G"],
    [1e6, "M"],
    [1e3, "k"],
    [1, ""],
    [1e-3, "m"],
    [1e-6, "µ"],
    [1e-9, "n"],
    [1e-12, "p"],
    [1e-15, "f"],
  ];
  for (const [scale, sym] of units) {
    if (a >= scale) {
      const s = `${trim(a / scale)}${sym}`;
      return neg ? `-${s}` : s;
    }
  }
  return (neg ? "-" : "") + a.toExponential(2);
}

function trim(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const s = Math.abs(v) >= 1000 || (Math.abs(v) < 1e-3 && v !== 0) ? v.toExponential(2) : v.toFixed(3);
  return s.replace(/\.?0+$/, "").replace(/\.?0+(e)/, "$1");
}
