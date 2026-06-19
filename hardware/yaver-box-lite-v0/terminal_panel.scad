// Yaver Box Lite V0 terminal / label panel.
// PART = "panel"

PART = "panel";
$fn = 36;

panel_l = 240;
panel_h = 46;
panel_t = 3;
hole_d = 3.2;

module panel(){
  difference(){
    cube([panel_l,panel_t,panel_h]);
    for(x=[8, 82, 158, 232])
      translate([x,-1,8]) rotate([-90,0,0]) cylinder(d=hole_d,h=panel_t+2);
  }
  // Label ledges.
  for(x=[0,60,130,190])
    translate([x, panel_t, 0]) cube([1.2, 3, panel_h]);

  labels = [
    ["POWER 24V", 30],
    ["RS485 MODBUS", 96],
    ["ROBOT USB/CAM", 160],
    ["CATPOWER", 216]
  ];
  for(l=labels)
    translate([l[1], panel_t+0.4, 30])
      rotate([90,0,0]) linear_extrude(0.8)
        text(l[0], size=5, halign="center", valign="center");

  translate([30,panel_t+0.4,12]) rotate([90,0,0]) linear_extrude(0.8)
    text("+24 0V PE", size=4, halign="center");
  translate([96,panel_t+0.4,12]) rotate([90,0,0]) linear_extrude(0.8)
    text("A B GND SHLD", size=4, halign="center");
  translate([160,panel_t+0.4,12]) rotate([90,0,0]) linear_extrude(0.8)
    text("ENDER FAIRINO CAM", size=4, halign="center");
  translate([216,panel_t+0.4,12]) rotate([90,0,0]) linear_extrude(0.8)
    text("BTS7960 FUSE KILL", size=4, halign="center");
}

panel();
