// Yaver Terminal-Block Nest (5.08mm pluggable terminal block) — parametric
// OpenSCAD. Holds an N-pin 5.08mm pluggable terminal-block PLUG so a robot can
// land a ferruled wire in each cage and drive the M2.5 screw to torque.
// docs/yaver-arm-served-harness-cell.md.
//
//   PART = "nest"  -> printable nest (gridded baseplate + plug cradle)
//
// Geometry of the real part (5.08mm female pluggable terminal block, rising-cage):
//   - wire enters the FRONT (cage mouths, +Y face) horizontally
//   - M2.5 screw driven from the TOP (+Z)
// So this nest opens BOTH faces: cage funnels on the front, screw windows on top.
// Ships UPRIGHT (wire-axis horizontal ⊥ screw-axis vertical) -> use tool-change
// (gripper inserts, screwdriver drives). For a one-cone approach, print this nest
// on a ~35° wedge spacer (Option A in the doc). Plug pose per cage is known by
// construction from the 5.08 pitch + the 6×6 grid feet.

PART = "nest";
$fn  = 56;
eps  = 0.1;

// ---- plug (from datasheet / compiler) ----
pins        = 6;
pitch       = 5.08;     // pin pitch
body_d      = 15;       // depth: front (cage) -> back
body_h      = 13;       // plug body height
end_margin  = 2.5;      // plastic beyond first/last pin
body_w      = (pins-1)*pitch + 2*end_margin;

cage_d      = 3.6;      // wire+ferrule clearance into the cage
cage_z      = 4;        // cage centre height above plug base
funnel_d    = 6.0;      // lead-in mouth for the ferrule
funnel_len  = 4;
screw_d     = 3.2;      // M2.5 head + driver-bit window
screw_setback = 4;      // screw axis set back from the front face

// ---- cradle / capture ----
wall        = 3;
floor_t     = 3;        // back-stop floor under the plug (insert force reacts here)
key_slot    = [2.4, 3]; // anti-rotation key on the back wall ([w,depth]; w=0 omit)

// ---- 6×6 grid mounting (matches formboard.scad) ----
grid_pitch  = 25;
base_t      = 4;
foot_hole_d = 5.3;      // M5 clearance
foot_cells  = [[0,0],[1,0]];

crd_w = body_w + 2*wall;
crd_d = body_d + wall + floor_t;     // wall at back, open front
crd_h = floor_t + body_h;

function colx(c) = c[0]*grid_pitch;
function coly(c) = c[1]*grid_pitch;
fx = [for (c=foot_cells) colx(c)];
fy = [for (c=foot_cells) coly(c)];
bp_x0 = min(min(fx) - grid_pitch/2, -crd_w/2);
bp_x1 = max(max(fx) + grid_pitch/2,  crd_w/2);
bp_y0 = min(min(fy) - grid_pitch/2, -crd_d/2);
bp_y1 = max(max(fy) + grid_pitch/2,  crd_d/2);

module baseplate() {
  translate([bp_x0, bp_y0, -base_t]) cube([bp_x1-bp_x0, bp_y1-bp_y0, base_t+eps]);
}
module mount_holes() {
  for (c = foot_cells)
    translate([colx(c), coly(c), -base_t-eps]) cylinder(d=foot_hole_d, h=base_t+2*eps);
}

// plug pocket: open top + open front (+Y), keyed at the back
module pocket() {
  translate([-body_w/2, -body_d/2, floor_t])
    cube([body_w, body_d + wall + eps, body_h + eps]);          // open front+top
  if (key_slot[0] > 0)
    translate([-key_slot[0]/2, body_d/2 - eps, floor_t])
      cube([key_slot[0], key_slot[1]+eps, body_h+eps]);
}

// per-pin cage funnels (front) + screw windows (top)
module openings() {
  ox = -(pins-1)*pitch/2;
  for (i = [0:pins-1]) {
    x = ox + i*pitch;
    // cage funnel: from the front face inward (-Y), at cage height
    translate([x, -body_d/2 - funnel_len, floor_t + cage_z]) rotate([-90,0,0]) {
      cylinder(d=cage_d, h=body_d+funnel_len+eps);
      cylinder(d1=funnel_d, d2=cage_d, h=funnel_len+eps);
    }
    // screw window: from the top down, set back from front
    translate([x, -body_d/2 + screw_setback, floor_t-eps])
      cylinder(d=screw_d, h=body_h+2*eps);
  }
}

module cradle() {
  difference() {
    translate([-crd_w/2, -body_d/2 - wall, 0]) cube([crd_w, crd_d, crd_h]);
    pocket();
    openings();
  }
  // self-fiducial dot at the pin-1 corner (vision position index)
  translate([-(pins-1)*pitch/2, -body_d/2 - wall + 1.4, crd_h]) cylinder(d=2.2, h=0.8);
}

module nest() {
  difference() { union() { baseplate(); cradle(); } mount_holes(); }
}

if (PART == "nest") nest();
