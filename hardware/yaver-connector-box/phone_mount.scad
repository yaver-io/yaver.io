// Yaver Connector Box — phone mount (camera positioning rig)
// OpenSCAD. In WIRELESS mode the phone is placed freely so its camera sees the
// whole machine while it talks to the box over Wi-Fi — this is that mount.
// Three printable parts:
//   PART="clamp"   -> spring-style phone clamp (adjustable jaw)
//   PART="ball"    -> 1" ball + stem (RAM-B size) to clip into clamp / arm
//   PART="adapter" -> 1/4"-20 tripod adapter + clip onto the box's wall-ear
//
// Pair the ball with an off-the-shelf RAM-B double-socket arm, or print the
// adapter to put the phone on any camera tripod aimed at the machine.

PART = "clamp";     // "clamp" | "ball" | "adapter"
$fn = 64;

// ---- phone clamp ----
phone_min = 62;     // min phone width (mm)
phone_max = 90;     // max phone width
jaw_d     = 14;     // grip depth
jaw_t     = 6;      // jaw thickness
back_t    = 5;

module jaw(){
  difference(){
    union(){
      cube([phone_max+2*jaw_t, back_t, 40]);                       // back plate
      translate([0,0,0]) cube([jaw_t, jaw_d+back_t, 40]);          // fixed jaw L
      translate([phone_max+jaw_t,0,0]) cube([jaw_t, jaw_d+back_t, 40]); // jaw R
    }
    // soft-grip cutouts + cable/camera relief slot in back
    translate([jaw_t+4, -1, 8]) cube([phone_max-8, back_t+2, 24]);
    // camera clearance window (so a rear-cam corner mount doesn't block lens)
    translate([phone_max-6, -1, 26]) cube([18, back_t+2, 12]);
    // USB-C cable relief (bottom centre)
    translate([(phone_max+2*jaw_t)/2-6, -1, -1]) cube([12, back_t+2, 10]);
  }
}

module ball_socket_neg(){
  // hollow socket for a 17 mm (1") ball (RAM-B). Subtract from back.
  translate([(phone_max+2*jaw_t)/2, back_t+9, 20]) sphere(d=17.4);
}

module clamp(){
  difference(){
    union(){
      jaw();
      // socket housing on the back
      translate([(phone_max+2*jaw_t)/2, back_t, 20]) rotate([-90,0,0]) cylinder(d=24, h=12);
    }
    ball_socket_neg();
    // socket mouth (slot so the ball clips in + tension)
    translate([(phone_max+2*jaw_t)/2, back_t+9, 20]) rotate([0,0,0]) cube([3, 30, 30], center=true);
  }
}

// ---- 1" ball + stem (RAM-B compatible) ----
module ball(){
  union(){
    sphere(d=17);
    translate([0,0,-12]) cylinder(d=10, h=12);
    translate([0,0,-12]) cylinder(d=22, h=4);     // base flange
  }
}

// ---- tripod / box-ear adapter ----
module adapter(){
  difference(){
    union(){
      cylinder(d=26, h=6);                         // base disk
      translate([0,0,6]) cylinder(d=10, h=12);     // stem to ball or socket
    }
    // 1/4"-20 thread clearance (use heat-set insert or tap)
    translate([0,0,-1]) cylinder(d=6.2, h=8);
    // M4 holes to bolt onto enclosure wall-ears
    for(a=[0:90:359]) rotate([0,0,a]) translate([10,0,-1]) cylinder(d=4.2, h=8);
  }
}

if(PART=="clamp") clamp();
else if(PART=="ball") ball();
else adapter();
