// WaveformChart — native interactive scope for circuit SimResults, drawn with
// react-native-svg. One trace per signal column straight from circuit_simulate;
// tap legend pills to toggle traces, drag across the plot for a cursor readout.
// Mirrors the web WaveformChart. Log-x for AC Bode.
import React, { useMemo, useState } from "react";
import { LayoutChangeEvent, Pressable, Text, View } from "react-native";
import Svg, { Circle, Line, Path, Rect, Text as SvgText } from "react-native-svg";

export type SimResult = {
  analysis: string;
  signals: string[];
  samples: number[][];
  engine?: string;
};

const PALETTE = ["#56b4ff", "#78dc82", "#ffaa46", "#eb6e96", "#be8cff", "#6edcdc", "#f4d35e", "#ef798a"];

export function WaveformChart({ result, height = 220 }: { result: SimResult; height?: number }) {
  const { signals, samples, analysis } = result;
  const logX = analysis === "ac";
  const [width, setWidth] = useState(340);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState<number | null>(null);

  const allCols = useMemo(
    () => signals.map((name, i) => ({ name, i })).filter((c) => c.i > 0 && !(logX && c.name.endsWith("deg"))),
    [signals, logX],
  );
  const visible = allCols.filter((c) => !hidden.has(c.name));

  const padL = 44;
  const padR = 8;
  const padT = 8;
  const padB = 22;
  const plotW = Math.max(40, width - padL - padR);
  const plotH = height - padT - padB;

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
  const colorOf = (name: string) => PALETTE[allCols.findIndex((c) => c.name === name) % PALETTE.length];

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
          d += `${pen ? "L" : "M"}${sx(row[0]).toFixed(1)} ${sy(v).toFixed(1)}`;
          pen = true;
        }
        return { name: c.name, d };
      }),
    [visible, samples, xmin, xmax, ymin, ymax, plotW, plotH],
  );

  const cursorRow = useMemo(() => {
    if (cursor == null || samples.length === 0) return null;
    let best = 0,
      bestD = Infinity;
    for (let r = 0; r < samples.length; r++) {
      const d = Math.abs(sx(samples[r][0]) - cursor);
      if (d < bestD) {
        bestD = d;
        best = r;
      }
    }
    return samples[best];
  }, [cursor, samples, xmin, xmax, plotW]);

  const onLayout = (e: LayoutChangeEvent) => setWidth(e.nativeEvent.layout.width);

  const yTicks = [0, 1, 2, 3, 4].map((i) => {
    const yv = ymin + ((ymax - ymin) * i) / 4;
    return { y: padT + plotH - (plotH * i) / 4, label: trim(yv) };
  });
  const xUnitShort = analysis === "tran" ? "s" : analysis === "ac" ? "Hz" : "x";
  const yUnit = analysis === "ac" ? "dB" : "V";

  return (
    <View onLayout={onLayout}>
      <Svg
        width="100%"
        height={height}
        onStartShouldSetResponder={() => true}
        onMoveShouldSetResponder={() => true}
        onResponderMove={(e) => {
          const x = e.nativeEvent.locationX;
          setCursor(x >= padL && x <= padL + plotW ? x : null);
        }}
        onResponderRelease={() => {}}
      >
        <Rect x={0} y={0} width={width} height={height} fill="#12141a" rx={8} />
        {yTicks.map((t, i) => (
          <React.Fragment key={i}>
            <Line x1={padL} y1={t.y} x2={padL + plotW} y2={t.y} stroke="#2c303a" strokeWidth={1} />
            <SvgText x={padL - 4} y={t.y + 3} fontSize={9} fill="#7a808c" textAnchor="end">
              {t.label}
            </SvgText>
          </React.Fragment>
        ))}
        {ymin < 0 && ymax > 0 && <Line x1={padL} y1={sy(0)} x2={padL + plotW} y2={sy(0)} stroke="#4a4f5a" strokeWidth={1} />}
        <Line x1={padL} y1={padT} x2={padL} y2={padT + plotH} stroke="#78808c" strokeWidth={1} />
        <Line x1={padL} y1={padT + plotH} x2={padL + plotW} y2={padT + plotH} stroke="#78808c" strokeWidth={1} />
        {paths.map((p) => (
          <Path key={p.name} d={p.d} fill="none" stroke={colorOf(p.name)} strokeWidth={1.6} />
        ))}
        {cursorRow && (
          <>
            <Line x1={sx(cursorRow[0])} y1={padT} x2={sx(cursorRow[0])} y2={padT + plotH} stroke="#ffffff55" strokeWidth={1} />
            {visible.map((c) => (
              <Circle key={c.name} cx={sx(cursorRow[0])} cy={sy(cursorRow[c.i])} r={2.5} fill={colorOf(c.name)} />
            ))}
          </>
        )}
        <SvgText x={padL + plotW} y={padT + plotH + 16} fontSize={9} fill="#5a606c" textAnchor="end">
          {yUnit} vs {xUnitShort}
          {logX ? " (log)" : ""}
        </SvgText>
      </Svg>

      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 8 }}>
        {allCols.map((c) => {
          const off = hidden.has(c.name);
          return (
            <Pressable
              key={c.name}
              onPress={() =>
                setHidden((h) => {
                  const n = new Set(h);
                  n.has(c.name) ? n.delete(c.name) : n.add(c.name);
                  return n;
                })
              }
              style={{
                flexDirection: "row",
                alignItems: "center",
                gap: 5,
                paddingHorizontal: 8,
                paddingVertical: 4,
                borderRadius: 6,
                backgroundColor: "#1c1f27",
                opacity: off ? 0.35 : 1,
              }}
            >
              <View style={{ width: 9, height: 9, borderRadius: 2, backgroundColor: colorOf(c.name) }} />
              <Text style={{ color: "#cfd3da", fontFamily: "Menlo", fontSize: 11 }}>{c.name}</Text>
              {cursorRow && !off && <Text style={{ color: "#8a909c", fontFamily: "Menlo", fontSize: 11 }}>{trim(cursorRow[c.i])}</Text>}
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

function trim(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const s = Math.abs(v) >= 1000 || (Math.abs(v) < 1e-3 && v !== 0) ? v.toExponential(2) : v.toFixed(3);
  return s.replace(/\.?0+$/, "");
}
