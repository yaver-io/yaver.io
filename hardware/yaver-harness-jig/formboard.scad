// Yaver Harness Formboard (formalama) — parametric, self-aware-ready base tile
// OpenSCAD. The datum'd, gridded carrier every nest/comb/fork bolts onto.
// docs/yaver-wire-harness-jig-formboard-design.md §A.
//
//   PART = "board"  -> the tile: grid of M5 insert holes + 3-2-1 datum + tag pocket
//   PART = "demo"   -> board + a few placed marker posts (preview only, don't print)
//
// The whole point: fixtures only ever sit at quantized grid cells, so their pose
// is known by construction. A 3-2-1 dowel datum fixes board->robot frame; an
// AprilTag pocket lets the eye-in-hand camera refine it each setup. The board-ID
// QR (printed label, dropped in the qr pocket) maps the tile to its harnessRecipe.
//
// Tile it: a 400x600 tile exceeds a 220 bed — set tile_x/tile_y to your bed and
// print N tiles that dowel together (the robot re-registers per tile via tag).

PART = "board";          // "board" | "demo"
$fn  = 48;

// ---- tile geometry ----
tile_x      = 200;       // X extent (set to your print bed; tiles dowel together)
tile_y      = 200;       // Y extent
plate_t     = 6;         // plate thickness
rib_h       = 8;         // underside rib height (stiffness); 0 = flat plate
rib_w       = 3;

// ---- fixture grid (optical-breadboard pattern) ----
grid_pitch  = 25;        // mm between mount cells
grid_margin = 12.5;      // edge offset to first cell (= half pitch -> centered)
insert_d    = 6.4;       // hole for M5 brass heat-set insert (use 5.3 for tapped/clearance)
insert_depth= 0;         // 0 = through hole; >0 = blind pocket from top

// ---- 3-2-1 kinematic datum (board origin in robot frame) ----
dowel_d     = 5.8;       // press-fit for Ø6 h6 steel dowel (printed undersize)
dowel_a     = [15, 15];          // primary  (X,Y)
dowel_b     = [tile_x-15, 15];   // along X  (sets rotation)
dowel_c     = [15, tile_y-15];   // along Y
clampbolt_d = 6.5;       // M6 clamp-to-table clearance

// ---- fiducial + id pockets ----
tag_xy      = [grid_margin, grid_margin]; // AprilTag pocket at cell (0,0) corner
tag_size    = 42;        // outer pocket; print a 36h11 tag at ~30mm inside
tag_depth   = 0.6;       // shallow recess to seat a printed/adhesive tag
qr_xy       = [tile_x-46, 6];
qr_size     = [40, 18];
qr_depth    = 0.6;

cols = floor((tile_x - 2*grid_margin) / grid_pitch) + 1;
rows = floor((tile_y - 2*grid_margin) / grid_pitch) + 1;

module grid_holes() {
  for (cx = [0:cols-1]) for (cy = [0:rows-1]) {
    translate([grid_margin + cx*grid_pitch, grid_margin + cy*grid_pitch, -1])
      cylinder(d=insert_d, h = (insert_depth>0 ? insert_depth+1 : plate_t+2));
  }
}

module datum_and_clamps() {
  for (p = [dowel_a, dowel_b, dowel_c])
    translate([p[0], p[1], -1]) cylinder(d=dowel_d, h=plate_t+2);
  // M6 clamp slots near corners (clamp the tile to the robot table)
  for (p = [[tile_x/2, 8], [tile_x/2, tile_y-8], [8, tile_y/2], [tile_x-8, tile_y/2]])
    translate([p[0], p[1], -1]) cylinder(d=clampbolt_d, h=plate_t+2);
}

module pockets() {
  translate([tag_xy[0]-tag_size/2, tag_xy[1]-tag_size/2, plate_t-tag_depth])
    cube([tag_size, tag_size, tag_depth+1]);
  translate([qr_xy[0], qr_xy[1], plate_t-qr_depth])
    cube([qr_size[0], qr_size[1], qr_depth+1]);
}

module ribs() {
  if (rib_h > 0)
    for (cx = [0:2:cols-1])                 // every other column line
      translate([grid_margin + cx*grid_pitch - rib_w/2, 4, -rib_h])
        cube([rib_w, tile_y-8, rib_h]);
}

module board() {
  difference() {
    union() {
      cube([tile_x, tile_y, plate_t]);
      ribs();
    }
    grid_holes();
    datum_and_clamps();
    pockets();
  }
}

if (PART == "board") board();
else {
  board();
  // preview a couple of marker posts on grid cells (do NOT print this)
  color("orange") for (c = [[2,3],[5,3],[5,6]])
    translate([grid_margin + c[0]*grid_pitch, grid_margin + c[1]*grid_pitch, plate_t])
      cylinder(d=6, h=14);
}
