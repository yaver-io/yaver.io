// Generic USB adapter clip for Yaver Box Lite V0.
// Measure the adapter and adjust body_w/body_h. Screw to base_plate bosses.
// PART = "clip"

PART = "clip";
$fn = 36;

clip_l = 62;
body_w = 24;
body_h = 14;
wall = 2.4;
base_t = 3;
screw_spacing = 44;

module clip(){
  difference(){
    union(){
      cube([clip_l, body_w + 2*wall, base_t]);
      translate([0,0,base_t]) cube([clip_l, wall, body_h]);
      translate([0,body_w+wall,base_t]) cube([clip_l, wall, body_h]);
      // front lip
      translate([0,0,base_t+body_h-2]) cube([clip_l, wall+4, 2]);
      translate([0,body_w+wall-4,base_t+body_h-2]) cube([clip_l, wall+4, 2]);
    }
    for(x=[clip_l/2-screw_spacing/2, clip_l/2+screw_spacing/2])
      translate([x, body_w/2+wall, -1]) cylinder(d=3.4, h=base_t+2);
    // cable tie passthrough under adapter.
    translate([clip_l/2-8, body_w/2+wall-2, -1]) cube([16,4,base_t+2]);
  }
}

clip();

