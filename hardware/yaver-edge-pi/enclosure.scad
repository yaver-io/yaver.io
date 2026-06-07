// Yaver Pi Edge — enclosure for a Raspberry Pi Zero 2 W + stacked RS485 board.
// OpenSCAD.  PART = "base" | "lid" | "both"
//
// Sized for the Pi Zero 2 W (65 x 30 mm, holes 58 x 23, M2.5). Long edge exposes
// the two micro-USB ports (power + data) + mini-HDMI; one short edge breaks out
// the RS485 3-pin screw terminal; SD card accessible on the other short edge.
// DIN-rail clip + wall ears. A small antenna/BLE keepout window keeps the Pi's
// onboard radio off the print's thickest wall (it still works enclosed, but the
// window helps in a metal panel).

PART = "both";
$fn = 48;

// Pi Zero 2 W
pi_l = 65; pi_w = 30; pi_t = 1.4;
hole_dx = 58; hole_dy = 23; hole_off = 3.5; hole_d = 2.75;

clear = 1.5; wall = 2.0; stand_h = 3.5;
inner_h = 20;                 // room for Pi + a stacked RS485 board
lip = 3;

in_l = pi_l + 2*clear; in_w = pi_w + 2*clear;
in_h = stand_h + pi_t + inner_h;
out_l = in_l + 2*wall; out_w = in_w + 2*wall; out_h = in_h + wall;

module rrect(l,w,r){ hull(){ for(sx=[r,l-r]) for(sy=[r,w-r]) translate([sx,sy]) circle(r=r);} }

module pi_standoffs(){
  // hole positions relative to the PCB corner
  xs = [hole_off, hole_off + hole_dx];
  ys = [hole_off, hole_off + hole_dy];
  for(x=xs) for(y=ys)
    translate([wall+clear+x, wall+clear+y, wall])
      difference(){ cylinder(d=6, h=stand_h); translate([0,0,-0.1]) cylinder(d=hole_d, h=stand_h+0.4); }
}

// long-edge port slot (micro-USB power+data + mini-HDMI region)
module port_slot(){
  // ports sit along the y=0 long edge, spanning x ~ 8..58 on the Pi
  z0 = wall + stand_h + pi_t - 0.5;
  translate([wall+clear+8, -1, z0]) cube([50, wall+2, 6]);
}

// short-edge RS485 terminal cutout (+x end)
module rs485_cut(){
  z0 = wall + stand_h + pi_t + 3;
  translate([out_l - wall - 1, out_w/2 - 8, z0]) cube([wall+2, 16, 8]);
}

// SD card slot (-x end)
module sd_cut(){
  z0 = wall + stand_h - 0.5;
  translate([-1, out_w/2 - 7, z0]) cube([wall+2, 14, 3]);
}

module din_clip(){
  din=35;
  translate([out_l/2-din/2, out_w, 0]){
    cube([din, 6, 10]);
    translate([0,6,8]) cube([din,4,6]);
    translate([0,6,-8]) cube([din,4,8]);
  }
}

module base(){
  difference(){
    union(){
      difference(){
        linear_extrude(out_h) rrect(out_l, out_w, 3);
        translate([wall, wall, wall]) linear_extrude(out_h) rrect(in_l, in_w, 2);
      }
      pi_standoffs();
      din_clip();
      // lid seat
      translate([wall-0.5, wall-0.5, out_h-lip]) difference(){
        linear_extrude(lip) rrect(in_l+1, in_w+1, 2);
        translate([0.7,0.7,-0.1]) linear_extrude(lip+0.2) rrect(in_l-0.4, in_w-0.4, 2);
      }
    }
    port_slot();
    rs485_cut();
    sd_cut();
  }
}

module lid(){
  difference(){
    union(){
      linear_extrude(wall) rrect(out_l, out_w, 3);
      translate([wall+0.3, wall+0.3, -lip+0.3]) difference(){
        linear_extrude(lip) rrect(in_l-0.6, in_w-0.6, 2);
        translate([0.8,0.8,-0.1]) linear_extrude(lip+0.2) rrect(in_l-2.2, in_w-2.2, 2);
      }
    }
    // vent slots + radio window + LED pipe + label
    for(i=[0:4]) translate([18+i*9, out_w/2-10, -1]) cube([3, 20, wall+2]);
    translate([out_l-14, out_w/2, -1]) cube([10, 16, wall+2]);   // BLE/Wi-Fi window over the Pi radio
    translate([10, 10, -1]) cylinder(d=2.2, h=wall+2);            // status LED pipe
    translate([out_l/2, out_w-9, wall-0.6]) linear_extrude(1)
      text("YAVER EDGE", size=5, halign="center", valign="center");
  }
}

if(PART=="base") base();
else if(PART=="lid") lid();
else { base(); translate([0,0,out_h+14]) lid(); }
