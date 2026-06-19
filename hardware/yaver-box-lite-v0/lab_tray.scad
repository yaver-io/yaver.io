// Removable low-voltage breadboard / MCP3008 tray.
// Keep this inside the box only for bench experiments.
// PART = "tray"

PART = "tray";
$fn = 36;

tray_l = 180;
tray_w = 70;
tray_t = 3;
wall = 3;

module rrect2d(l,w,r){
  hull(){ for(x=[r,l-r]) for(y=[r,w-r]) translate([x,y]) circle(r=r); }
}

module tray(){
  difference(){
    linear_extrude(tray_t) rrect2d(tray_l,tray_w,4);
    for(p=[[10,10],[tray_l-10,10],[10,tray_w-10],[tray_l-10,tray_w-10]])
      translate([p[0],p[1],-1]) cylinder(d=3.4,h=tray_t+2);
  }
  // Breadboard stop rails.
  translate([8,8,tray_t]) cube([tray_l-16,wall,5]);
  translate([8,tray_w-8-wall,tray_t]) cube([tray_l-16,wall,5]);
  translate([8,8,tray_t]) cube([wall,tray_w-16,5]);
  translate([tray_l-8-wall,8,tray_t]) cube([wall,tray_w-16,5]);

  translate([tray_l/2,tray_w/2,tray_t+0.2])
    linear_extrude(0.8)
      text("LOW VOLTAGE ONLY - MCP3008 / BREADBOARD", size=5,
           halign="center", valign="center");
}

tray();

