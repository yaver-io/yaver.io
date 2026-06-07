// MeshIcons.tsx — hand-rolled inline SVG icons for the Yaver Mesh surface.
// Per project convention (feedback_no_lucide_use_inline_svg) we do NOT pull in
// an icon library: feather-style, stroke 1.6, currentColor via the `color` prop.

import React from "react";
import Svg, { Circle, Line, Path, Polyline, Rect } from "react-native-svg";

type IconProps = { size?: number; color?: string; strokeWidth?: number };

const base = (size: number) => ({ width: size, height: size, viewBox: "0 0 24 24" });

/** Upward arrow out of a circle — an exit node (egress to the internet). */
export function ExitNodeIcon({ size = 18, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Circle cx="12" cy="12" r="9" stroke={color} strokeWidth={strokeWidth} />
      <Line x1="12" y1="16" x2="12" y2="8" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" />
      <Polyline points="8.5 11.5 12 8 15.5 11.5" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
    </Svg>
  );
}

/** Two-way branch — a gateway / subnet router bridging another network. */
export function GatewayIcon({ size = 18, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Path d="M4 7h7l4 0M4 17h7l4 0" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" />
      <Polyline points="13 4 17 7 13 10" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
      <Polyline points="13 14 17 17 13 20" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
      <Line x1="20" y1="7" x2="20" y2="17" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" />
    </Svg>
  );
}

export function CopyIcon({ size = 16, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Rect x="9" y="9" width="11" height="11" rx="2" stroke={color} strokeWidth={strokeWidth} />
      <Path d="M5 15V5a2 2 0 0 1 2-2h10" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" />
    </Svg>
  );
}

export function SearchIcon({ size = 16, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Circle cx="11" cy="11" r="7" stroke={color} strokeWidth={strokeWidth} />
      <Line x1="16.5" y1="16.5" x2="21" y2="21" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" />
    </Svg>
  );
}

export function ChevronRightIcon({ size = 18, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Polyline points="9 6 15 12 9 18" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
    </Svg>
  );
}

export function CheckIcon({ size = 16, color = "currentColor", strokeWidth = 2 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Polyline points="5 12.5 10 17 19 7" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
    </Svg>
  );
}

/** Globe — the mesh itself / overlay. */
export function MeshIcon({ size = 18, color = "currentColor", strokeWidth = 1.6 }: IconProps) {
  return (
    <Svg {...base(size)} fill="none">
      <Circle cx="12" cy="12" r="9" stroke={color} strokeWidth={strokeWidth} />
      <Line x1="3" y1="12" x2="21" y2="12" stroke={color} strokeWidth={strokeWidth} />
      <Path d="M12 3c2.5 2.5 3.8 5.7 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.7-3.8-9S9.5 5.5 12 3Z" stroke={color} strokeWidth={strokeWidth} />
    </Svg>
  );
}
