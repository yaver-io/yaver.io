// Yaver Physical KVM v0 — parametric two-piece ventilated enclosure.
// OpenSCAD: set part="base" or "lid", F6, Export STL.
// Dimensions are millimetres. Capture sticks vary; measure yours and override
// capture_* before printing. The design uses commodity modules, no custom PCB.

part = "assembly"; // "base", "lid", "assembly"

wall = 2.4;
floor_t = 2.4;
clearance = 0.45;
corner_r = 5;
inner_x = 130;
inner_y = 110;
base_h = 27;
lid_h = 8;

// Raspberry Pi 4B envelope and standard mounting-hole centres.
pi_x = 85;
pi_y = 56;
pi_hole_dx = 58;
pi_hole_dy = 49;
pi_hole_d = 2.75;
pi_origin = [10, 10];

// Generic USB HDMI capture stick bay (override for the chosen production SKU).
capture_x = 65;
capture_y = 29;
capture_z = 15;
capture_origin = [4, 72];

// M5Stack AtomS3U envelope. Mounted near front so button/LED remain visible.
m5_x = 53;
m5_y = 20;
m5_z = 10.5;
m5_origin = [72, 74];

screw_d = 3.2;
insert_d = 4.6; // M3 heat-set insert pilot; tune for filament/insert.
vent_d = 4.2;
vent_pitch = 8;

// Fail at render time when a production-SKU override creates an impossible
// arrangement. Rails add 2 mm beyond each nominal module envelope.
assert(pi_origin[1] + pi_y + 5 <= capture_origin[1],
       "Pi and capture-card service envelopes overlap");
assert(pi_origin[0] >= 8 && pi_origin[1] >= 8 &&
       pi_origin[0] + pi_x <= inner_x && pi_origin[1] + pi_y <= inner_y,
       "Pi envelope collides with a wall or front lid tower");
assert(capture_origin[0] + capture_x + 2 <= m5_origin[0],
       "Capture-card and AtomS3U cradles overlap");
assert(capture_origin[0] + capture_x + 4 <= inner_x &&
       capture_origin[1] + capture_y <= inner_y - 9,
       "Capture-card cradle exceeds the enclosure interior");
assert(m5_origin[0] + m5_x + 2 <= inner_x &&
       m5_origin[1] + m5_y + 2 <= inner_y,
       "AtomS3U cradle exceeds the enclosure interior");

module rounded_box(size, radius) {
  hull() for (x=[radius,size[0]-radius], y=[radius,size[1]-radius])
    translate([x,y,0]) cylinder(h=size[2], r=radius, $fn=36);
}

module shell(open_top=true) {
  difference() {
    rounded_box([inner_x+2*wall, inner_y+2*wall, base_h+floor_t], corner_r);
    translate([wall,wall,floor_t])
      rounded_box([inner_x,inner_y,base_h+(open_top?2:0)], max(1,corner_r-wall));
  }
}

module standoff(pos, height=5, outer=6, hole=pi_hole_d) {
  translate([pos[0],pos[1],floor_t]) difference() {
    cylinder(h=height, d=outer, $fn=28);
    translate([0,0,-0.1]) cylinder(h=height+0.2, d=hole, $fn=24);
  }
}

module base() {
  difference() {
    union() {
      shell();
      // Pi 4 standoffs.
      for (x=[0,pi_hole_dx], y=[0,pi_hole_dy])
        standoff([wall+pi_origin[0]+3.5+x, wall+pi_origin[1]+3.5+y]);
      // Capture-card adjustable cradle rails; foam tape or zip tie holds card.
      for (y=[capture_origin[1], capture_origin[1]+capture_y+2])
        translate([wall+capture_origin[0],wall+y,floor_t]) cube([capture_x+4,2,5]);
      // AtomS3U cradle rails. Its USB-A extension exits the right panel.
      for (y=[m5_origin[1],m5_origin[1]+m5_y+2])
        translate([wall+m5_origin[0],wall+y,floor_t]) cube([m5_x+2,2,4]);
      // Four lid insert towers.
      for (p=[[5,5],[inner_x-5,5],[5,inner_y-5],[inner_x-5,inner_y-5]])
        translate([wall+p[0],wall+p[1],floor_t]) cylinder(h=base_h-2,d=7,$fn=28);
    }
    // Lid screw pilots through insert towers.
    for (p=[[5,5],[inner_x-5,5],[5,inner_y-5],[inner_x-5,inner_y-5]])
      translate([wall+p[0],wall+p[1],base_h-3]) cylinder(h=6,d=insert_d,$fn=24);

    // Pi connector wall: USB-C power, 2x micro-HDMI service, audio.
    translate([-0.1,wall+8,7]) cube([wall+0.2,13,10]);
    translate([-0.1,wall+25,7]) cube([wall+0.2,30,11]);
    // Pi Ethernet/USB bank at rear.
    translate([wall+70,inner_y+wall-0.1,6]) cube([49,wall+0.2,18]);
    // External target HDMI input, panel extension from capture card.
    translate([wall+10,-0.1,8]) cube([18,wall+0.2,10]);
    // External M5 USB-HID cable to target PC.
    translate([inner_x+wall-0.1,wall+75,7]) cube([wall+0.2,13,10]);
    // M5 arm button finger access and LED sight line.
    translate([inner_x+wall-0.1,wall+88,10]) rotate([0,90,0]) cylinder(h=wall+0.2,d=9,$fn=30);

    // Low side intake vents, both long walls.
    for (x=[35:10:105]) {
      translate([wall+x,-0.1,8]) rotate([-90,0,0]) cylinder(h=wall+0.2,d=4,$fn=20);
      translate([wall+x,inner_y+wall-0.1,8]) rotate([-90,0,0]) cylinder(h=wall+0.2,d=4,$fn=20);
    }
  }
}

module lid() {
  difference() {
    union() {
      rounded_box([inner_x+2*wall,inner_y+2*wall,lid_h],corner_r);
      // Inner locating lip; interrupted at corners for easy fit.
      translate([wall+clearance,wall+clearance,0])
        difference() {
          rounded_box([inner_x-2*clearance,inner_y-2*clearance,3],max(1,corner_r-wall));
          translate([1.5,1.5,-0.1])
            rounded_box([inner_x-2*clearance-3,inner_y-2*clearance-3,3.2],max(1,corner_r-wall-1.5));
        }
    }
    // Exhaust field over Pi/capture. Keep a solid perimeter and logo zone.
    for (x=[16:vent_pitch:inner_x-8], y=[14:vent_pitch:inner_y-10])
      if (!(x>78 && x<118 && y>72))
        translate([wall+x,wall+y,-0.1]) cylinder(h=lid_h+0.2,d=vent_d,$fn=20);
    // Four M3 lid screws.
    for (p=[[5,5],[inner_x-5,5],[5,inner_y-5],[inner_x-5,inner_y-5]])
      translate([wall+p[0],wall+p[1],-0.1]) cylinder(h=lid_h+0.2,d=screw_d,$fn=24);
    // Embossed-recessed simple product mark (font availability independent).
    translate([wall+83,wall+79,lid_h-0.8]) linear_extrude(1) text("YAVER KVM",size=5,halign="center",font="sans:style=Bold");
  }
}

if (part == "base") base();
else if (part == "lid") lid();
else {
  base();
  translate([0,0,base_h+floor_t+1]) lid();
}
