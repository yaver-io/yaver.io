// Yaver Box Lite V0 base plate.
// OpenSCAD. PART = "plate" | "preview"
//
// One reusable Pi 4 box for OCPP/PZEM, JCWelec listening, and Simkab robotics.
// This is an internal mounting plate for a larger enclosure or open DIN test rig.

PART = "preview";
$fn = 36;

// Overall plate.
plate_l = 260;
plate_w = 170;
plate_t = 4;
corner_r = 4;

// Raspberry Pi 4 mounting, approximate board 85 x 56 mm, M2.5 holes.
pi_l = 85;
pi_w = 56;
pi_holes = [[3.5,3.5], [61.5,3.5], [3.5,52.5], [61.5,52.5]];
pi_x = 92;
pi_y = 58;
standoff_h = 7;

// EDR-120-24 placeholder zone. Verify real supply and DIN clearance.
psu_x = 12;
psu_y = 34;
psu_l = 55;
psu_w = 125;

// Terminal panel screw holes.
panel_x = 18;
panel_y = 10;
panel_l = 224;

// USB screwdriver companion placeholder (Arduino Nano/Pico-class).
comp_x = 92;
comp_y = 122;
comp_l = 58;
comp_w = 28;

module rrect2d(l,w,r){
  hull(){
    for(x=[r,l-r]) for(y=[r,w-r]) translate([x,y]) circle(r=r);
  }
}

module slot(l=16,w=4,h=plate_t+2){
  translate([-l/2,-w/2,-1]) cube([l,w,h]);
}

module plate_body(){
  linear_extrude(plate_t) rrect2d(plate_l, plate_w, corner_r);
}

module mounting_holes(){
  // Corner enclosure/DIN-plate mounting holes, M4 clearance.
  for(p=[[10,10],[plate_l-10,10],[10,plate_w-10],[plate_l-10,plate_w-10]])
    translate([p[0],p[1],-1]) cylinder(d=4.4,h=plate_t+2);
}

module cable_tie_slots(){
  // Rear cable comb slots.
  for(x=[50:28:230]) translate([x, plate_w-14, 0]) slot(16,4);
  // USB adapter tie-down slots.
  for(x=[188,225]) for(y=[58,92,126]) translate([x,y,0]) rotate([0,0,90]) slot(18,4);
  // PSU wiring slots.
  for(y=[55,95,135]) translate([psu_x+psu_l+12,y,0]) rotate([0,0,90]) slot(18,4);
}

module pi_standoffs(){
  for(p=pi_holes)
    translate([pi_x+p[0], pi_y+p[1], plate_t])
      difference(){
        cylinder(d=7, h=standoff_h);
        translate([0,0,-0.2]) cylinder(d=2.7, h=standoff_h+0.4);
      }
}

module screw_boss(x,y,d=7,h=5,hole=3.2){
  translate([x,y,plate_t])
    difference(){
      cylinder(d=d,h=h);
      translate([0,0,-0.2]) cylinder(d=hole,h=h+0.4);
    }
}

module panel_bosses(){
  for(x=[panel_x, panel_x+panel_l/3, panel_x+2*panel_l/3, panel_x+panel_l])
    screw_boss(x, panel_y, 8, 5, 3.2);
}

module psu_zone(){
  // Raised border only. Mount real DIN rail or PSU bracket inside this zone.
  translate([psu_x,psu_y,plate_t])
    difference(){
      cube([psu_l, psu_w, 3]);
      translate([3,3,-0.2]) cube([psu_l-6, psu_w-6, 3.4]);
    }
  translate([psu_x+psu_l/2, psu_y+psu_w+8, plate_t+0.2])
    linear_extrude(0.8) text("EDR-120-24 ZONE", size=6, halign="center");
}

module usb_clip_bosses(){
  // Two generic adapter clip positions. Use usb_adapter_clip.scad mounted here.
  for(pos=[[190,54],[190,94],[222,54],[222,94]])
    screw_boss(pos[0], pos[1], 7, 5, 3.2);
}

module companion_zone(){
  translate([comp_x,comp_y,plate_t])
    difference(){
      cube([comp_l, comp_w, 3]);
      translate([3,3,-0.2]) cube([comp_l-6, comp_w-6, 3.4]);
    }
  for(pos=[[comp_x+7,comp_y+7],[comp_x+comp_l-7,comp_y+7],[comp_x+7,comp_y+comp_w-7],[comp_x+comp_l-7,comp_y+comp_w-7]])
    screw_boss(pos[0], pos[1], 5.5, 4, 2.8);
  translate([comp_x+comp_l/2, comp_y+comp_w+6, plate_t+0.2])
    linear_extrude(0.8) text("USB SCREWDRIVER COMPANION", size=4.5, halign="center");
}

module labels(){
  translate([pi_x+pi_l/2, pi_y-9, plate_t+0.2])
    linear_extrude(0.8) text("RASPBERRY PI 4", size=6, halign="center");
  translate([205,132,plate_t+0.2])
    linear_extrude(0.8) text("USB / RS485 / HUB", size=6, halign="center");
  translate([plate_l/2,5,plate_t+0.2])
    linear_extrude(0.8) text("FRONT TERMINAL PANEL", size=5, halign="center");
}

module plate(){
  difference(){
    plate_body();
    mounting_holes();
    cable_tie_slots();
  }
  pi_standoffs();
  panel_bosses();
  psu_zone();
  usb_clip_bosses();
  companion_zone();
  labels();
}

if(PART=="plate") plate();
else {
  plate();
  // Ghost footprints.
  %translate([pi_x,pi_y,plate_t+standoff_h]) cube([pi_l,pi_w,1.6]);
  %translate([psu_x,psu_y,plate_t+4]) cube([psu_l,psu_w,42]);
  %translate([comp_x,comp_y,plate_t+5]) cube([comp_l,comp_w,2]);
}
