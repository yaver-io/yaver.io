// Yaver Connector Nest (rectangular cavity array) — parametric generator
// OpenSCAD. One generator -> a nest for any rectangular-array crimp housing or
// terminal block. docs/yaver-wire-harness-jig-formboard-design.md §C.
//
//   PART = "nest"  -> printable nest (flat gridded baseplate + housing cradle)
//
// Per-harness the compiler emits the parameters below from the connector BOM
// (docs/yaver-harness-compiler-design.md). Design intent baked in:
//   - housing drops into a TOP-opening pocket, captured + keyed (anti-rotation)
//   - the pocket mouth is CHAMFERED -> lead-in that forgives ~+/-0.5mm robot
//     error on the way in (F/T spiral search does the rest)
//   - a BACK-STOP floor under the housing reacts insertion force into the fixture
//   - a flat GRIDDED BASEPLATE on the 25mm M5 pattern connects everything and
//     fixes the nest's pose by construction (no measuring)
//   - a self-fiducial dot (cavity-1 corner) so vision indexes cavities reliably
// Vertical insertion (robot from above). For a tilted presentation, print on a
// wedge spacer or set wedge_deg>0 (adds a wedge under the cradle, baseplate flat).

PART = "nest";
$fn  = 56;
eps  = 0.1;

// ---- cavity array (from the connector datasheet / compiler) ----
cav_rows    = 2;
cav_cols    = 3;
cav_pitch_x = 5.7;       // mm between cavities, X (e.g. Molex Mega-Fit 5.7)
cav_pitch_y = 5.7;       // mm between cavities, Y

// ---- housing capture pocket ----
body_x      = cav_cols*cav_pitch_x + 4;   // housing footprint X (+slop)
body_y      = cav_rows*cav_pitch_y + 4;   // housing footprint Y (+slop)
body_h      = 10;        // capture depth
floor_t     = 3;         // back-stop floor under the housing
chamfer     = 2.5;       // lead-in chamfer at the pocket mouth
key_slot    = [2.4, 4];  // anti-rotation key [w, depth] on +Y wall (w=0 to omit)
wall        = 3;         // cradle wall thickness

// ---- presentation / mounting ----
wedge_deg   = 0;         // >0 tilts the cradle toward the robot (baseplate stays flat)
grid_pitch  = 25;
base_t      = 4;
foot_hole_d = 5.3;       // M5 clearance into the board insert
foot_cells  = [[0,0],[1,0]];   // grid cells this nest spans (compiler-set)

crd_x = body_x + 2*wall;
crd_y = body_y + 2*wall;
crd_h = floor_t + body_h;

function colx(c) = c[0]*grid_pitch;
function coly(c) = c[1]*grid_pitch;
fx = [for (c=foot_cells) colx(c)];
fy = [for (c=foot_cells) coly(c)];
// baseplate bounding box: spans every foot cell AND the cradle footprint
bp_x0 = min(min(fx) - grid_pitch/2, -crd_x/2);
bp_x1 = max(max(fx) + grid_pitch/2,  crd_x/2);
bp_y0 = min(min(fy) - grid_pitch/2, -crd_y/2);
bp_y1 = max(max(fy) + grid_pitch/2,  crd_y/2);

module baseplate() {
  translate([bp_x0, bp_y0, -base_t])
    cube([bp_x1-bp_x0, bp_y1-bp_y0, base_t+eps]);
}

module mount_holes() {
  for (c = foot_cells)
    translate([colx(c), coly(c), -base_t-eps]) cylinder(d=foot_hole_d, h=base_t+2*eps);
}

// upward-opening pocket with a chamfered mouth + optional key slot
module pocket() {
  translate([0,0,floor_t]) {
    cube([body_x, body_y, body_h+eps], center=true);     // straight capture
    // chamfered mouth (frustum widening to the top)
    translate([0,0,body_h-chamfer])
      hull() {
        cube([body_x, body_y, eps], center=true);
        translate([0,0,chamfer]) cube([body_x+2*chamfer, body_y+2*chamfer, eps], center=true);
      }
    if (key_slot[0] > 0)
      translate([0, body_y/2, body_h/2])
        cube([key_slot[0], key_slot[1]*2, body_h+eps], center=true);
  }
}

module cradle() {
  difference() {
    translate([0,0,crd_h/2]) cube([crd_x, crd_y, crd_h], center=true);
    pocket();
  }
  // self-fiducial dot at the cavity-1 corner (vision cavity index)
  translate([-body_x/2+1.5, -body_y/2+1.5, crd_h]) cylinder(d=2.4, h=0.8);
}

module nest() {
  difference() {
    union() {
      baseplate();
      if (wedge_deg > 0)
        rotate([wedge_deg,0,0]) cradle();
      else
        cradle();
    }
    mount_holes();
  }
}

if (PART == "nest") nest();
