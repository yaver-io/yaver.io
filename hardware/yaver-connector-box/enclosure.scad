// Yaver Connector Box — parametric enclosure (base + lid)
// OpenSCAD. Renders the electronics enclosure that fits the Rev-B PCB.
// Set PART below and F6-render, then export STL per part.
//
//   PART = "base"  -> bottom shell + PCB standoffs + wall cutouts
//   PART = "lid"   -> top cover + vents + antenna boss + light pipes
//   PART = "both"  -> exploded preview (do not export)
//
// Connector cutouts assume the PCB edge map in pcb.md:
//   FRONT wall (y=0): USB-C (phone), USB-A (passthrough), antenna u.FL/SMA
//   BACK  wall (y=W): RS485 term, RS232 term, CAN term, DC jack
// Adjust the *_x offsets if you move connectors on the PCB.

PART = "both";          // "base" | "lid" | "both"
$fn = 48;

// ---- PCB / interior ----
pcb_l   = 100;          // PCB length (X)
pcb_w   = 80;           // PCB width  (Y)
pcb_t   = 1.6;
clear   = 2.0;          // clearance PCB->wall
wall    = 2.4;
stand_h = 4.0;          // standoff height (under-board clearance)
inner_h = 30;           // interior height above PCB top
lip     = 4;            // lid lip engagement

in_l = pcb_l + 2*clear;             // interior X
in_w = pcb_w + 2*clear;             // interior Y
in_h = stand_h + pcb_t + inner_h;   // interior Z

out_l = in_l + 2*wall;
out_w = in_w + 2*wall;
out_h = in_h + wall;                // base floor only; lid adds top

// ---- mounting ----
screw_d   = 3.2;        // M3 clearance
boss_d    = 7;
din_w     = 35;         // DIN-rail width

// PCB mounting hole positions (relative to PCB origin), match pcb.md
holes = [[4,4],[pcb_l-4,4],[4,pcb_w-4],[pcb_l-4,pcb_w-4]];

// ---- connector cutouts: [centre_x_along_wall, z_centre, width, height] ----
// FRONT (y=0)
usbc_front = [22, stand_h+pcb_t+3.5, 10, 4];     // USB-C
usba_front = [48, stand_h+pcb_t+6.5, 14, 7.5];   // USB-A
ant_front  = [pcb_l-14, stand_h+pcb_t+10, 7, 7]; // antenna SMA/u.FL hole (round)
// BACK (y=W)
rs485_back = [18, stand_h+pcb_t+6, 14, 9];        // RS485 screw terminal
rs232_back = [40, stand_h+pcb_t+6, 14, 9];        // RS232 screw terminal
dcjk_back  = [pcb_l-16, stand_h+pcb_t+5, 10, 9];  // DC jack + power terminal

module rrect(l,w,r){ // rounded rectangle prism in XY, height given by extrude caller
  hull(){
    for(sx=[r, l-r]) for(sy=[r, w-r]) translate([sx,sy,0]) circle(r=r);
  }
}

module shell_outer(h){
  linear_extrude(h) rrect(out_l, out_w, 3);
}

// cutout on a wall: side = "front"(y=0) | "back"(y=max)
module wall_cut(side, spec, round=false){
  cx = spec[0]; cz = spec[1]; cw = spec[2]; ch = spec[3];
  // map PCB x -> outer x (PCB origin sits at [wall+clear, wall+clear])
  ox = wall + clear + cx;
  yfront = -1; yback = out_w+1; depth = wall+2;
  translate([ox, side=="front" ? yfront : yback - depth, wall+cz])
    rotate([0,0,0])
      if(round) translate([0,0,0]) rotate([-90,0,0]) cylinder(h=depth, d=cw, center=false);
      else translate([-cw/2, 0, -ch/2]) cube([cw, depth, ch]);
}

module standoffs(){
  for(h=holes) translate([wall+clear+h[0], wall+clear+h[1], wall])
    difference(){
      cylinder(d=boss_d, h=stand_h);
      translate([0,0,-0.1]) cylinder(d=2.6, h=stand_h+0.2); // pilot for M3 self-tap
    }
}

module vents(){
  // slot vents on the lid top
  for(i=[0:5]) translate([wall+clear+15+i*12, out_w/2-12, -1])
    cube([3, 24, wall+2]);
}

module din_clip(){
  // simplified DIN35 hook block on the back, printable as part of base
  translate([out_l/2-din_w/2, out_w, 0]) {
    cube([din_w, 6, 10]);
    translate([0, 6, 8]) cube([din_w, 4, 6]);  // upper hook lip
    translate([0, 6, -8]) cube([din_w, 4, 8]); // lower fixed lip
  }
}

module wall_ears(){
  for(x=[-6, out_l]) translate([x, out_w*0.25, 0])
    difference(){
      hull(){ cube([6, 14, 5]); translate([3,7,0]) cylinder(d=14,h=5); }
      translate([3,7,-1]) cylinder(d=4.2, h=8); // M4 wall mount
    }
}

module base(){
  difference(){
    union(){
      // outer shell, open top
      difference(){
        shell_outer(out_h);
        translate([wall, wall, wall]) linear_extrude(out_h) rrect(in_l, in_w, 2);
      }
      standoffs();
      din_clip();
      wall_ears();
      // lid lip seat
      translate([wall-0.6, wall-0.6, out_h-lip]) difference(){
        linear_extrude(lip) rrect(in_l+1.2, in_w+1.2, 2);
        translate([0.8,0.8,-0.1]) linear_extrude(lip+0.2) rrect(in_l-0.4, in_w-0.4, 2);
      }
    }
    // wall cutouts
    wall_cut("front", usbc_front);
    wall_cut("front", usba_front);
    wall_cut("front", ant_front, round=true);
    wall_cut("back",  rs485_back);
    wall_cut("back",  rs232_back);
    wall_cut("back",  dcjk_back);
  }
}

module lid(){
  difference(){
    union(){
      linear_extrude(wall) rrect(out_l, out_w, 3);
      // inner lip plug
      translate([wall+0.2, wall+0.2, -lip+0.4])
        difference(){
          linear_extrude(lip) rrect(in_l-0.4, in_w-0.4, 2);
          translate([1,1,-0.1]) linear_extrude(lip+0.2) rrect(in_l-2.4, in_w-2.4, 2);
        }
      // antenna boss (raised, for SMA bulkhead if used on lid instead)
      translate([out_l-16, out_w/2, 0]) cylinder(d=10, h=4);
    }
    vents();
    // light pipes / LED windows
    for(i=[0:2]) translate([14+i*7, 12, -1]) cylinder(d=2.2, h=wall+2);
    // antenna hole in boss
    translate([out_l-16, out_w/2, -1]) cylinder(d=6.5, h=8);
    // label pocket (engrave "YAVER" — shallow)
    translate([out_l/2, out_w-14, wall-0.6])
      linear_extrude(1) text("YAVER", size=7, halign="center", valign="center");
  }
}

if(PART=="base") base();
else if(PART=="lid") lid();
else { // both: exploded preview
  base();
  translate([0,0,out_h+18]) lid();
  // ghost PCB
  %translate([wall+clear, wall+clear, wall+stand_h]) cube([pcb_l, pcb_w, pcb_t]);
}
