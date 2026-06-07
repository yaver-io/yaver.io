// Yaver Wireless RS485 Stick (Kit K2) — enclosure
// OpenSCAD. A thumb-sized box for the ESP32-S3 SoftAP RS485 gateway: USB-C power
// at one end, a 3-pin screw terminal (A/B/GND) at the other, u.FL antenna hole.
//   PART = "base" | "lid" | "both"
PART = "both";
$fn = 48;

// PCB
pcb_l = 64; pcb_w = 22; pcb_t = 1.6;
clear = 1.2; wall = 1.8; stand_h = 2.5; inner_h = 11;

in_l = pcb_l + 2*clear;
in_w = pcb_w + 2*clear;
in_h = stand_h + pcb_t + inner_h;
out_l = in_l + 2*wall;
out_w = in_w + 2*wall;
out_h = in_h + wall;
lip = 3;

// cutouts [centre_along_wall, z_centre, w, h]
usbc = [out_w/2, wall+stand_h+pcb_t+2.5, 10, 3.6];     // USB-C, on the -X end wall
term = [out_w/2, wall+stand_h+pcb_t+3.5, 16, 7];        // 3-pin screw terminal, +X end wall
ant  = [out_w-7, wall+stand_h+pcb_t+5, 6.5];            // antenna hole (round), top-ish on +X end

module rrect(l,w,r){ hull(){ for(sx=[r,l-r]) for(sy=[r,w-r]) translate([sx,sy]) circle(r=r);} }

module standoffs(){
  for(p=[[6,6],[pcb_l-6,6],[6,pcb_w-6],[pcb_l-6,pcb_w-6]])
    translate([wall+clear+p[0], wall+clear+p[1], wall])
      difference(){ cylinder(d=4.5,h=stand_h); translate([0,0,-0.1]) cylinder(d=1.8,h=stand_h+0.2); }
}

// cut on an end wall: end = "minus"(x=0) | "plus"(x=out_l)
module end_cut(end, spec, round=false){
  cy = spec[0]; cz = spec[1]; cw = spec[2]; ch = (len(spec)>3)?spec[3]:0;
  depth = wall+2;
  x0 = (end=="minus") ? -1 : out_l - depth + 1;
  translate([x0, cy, cz])
    if(round) rotate([0,90,0]) cylinder(h=depth, d=cw);
    else translate([0, -cw/2, -ch/2]) cube([depth, cw, ch]);
}

module base(){
  difference(){
    union(){
      difference(){
        linear_extrude(out_h) rrect(out_l,out_w,2.5);
        translate([wall,wall,wall]) linear_extrude(out_h) rrect(in_l,in_w,1.5);
      }
      standoffs();
      // lid seat
      translate([wall-0.5,wall-0.5,out_h-lip]) difference(){
        linear_extrude(lip) rrect(in_l+1,in_w+1,1.5);
        translate([0.7,0.7,-0.1]) linear_extrude(lip+0.2) rrect(in_l-0.4,in_w-0.4,1.5);
      }
    }
    end_cut("minus", usbc);
    end_cut("plus",  term);
    end_cut("plus",  ant, round=true);
  }
}

module lid(){
  difference(){
    union(){
      linear_extrude(wall) rrect(out_l,out_w,2.5);
      translate([wall+0.3,wall+0.3,-lip+0.3]) difference(){
        linear_extrude(lip) rrect(in_l-0.6,in_w-0.6,1.5);
        translate([0.8,0.8,-0.1]) linear_extrude(lip+0.2) rrect(in_l-2.2,in_w-2.2,1.5);
      }
    }
    // status-LED light pipe + vent
    translate([out_l/2, out_w/2, -1]) cylinder(d=2.2, h=wall+2);
    translate([out_l/2, out_w-12, wall-0.5]) linear_extrude(1)
      text("YAVER 485", size=4, halign="center");
  }
}

if(PART=="base") base();
else if(PART=="lid") lid();
else { base(); translate([0,0,out_h+12]) lid(); }
