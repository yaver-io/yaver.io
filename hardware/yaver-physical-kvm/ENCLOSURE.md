# V0 enclosure

The v0 product is a serviceable printed enclosure around three unmodified,
replaceable modules: Raspberry Pi 4B 4 GB, one qualified UVC HDMI capture card,
and M5Stack AtomS3U. Source and CAD are open; Yaver can sell a factory-flashed,
assembled, burn-in-tested kit plus optional managed tunnel/support.

`enclosure/yaver_kvm_v0.scad` generates the base and lid. The capture-card bay
and connector openings are parameters because commodity cards are not a stable
mechanical standard. Freeze one supplier SKU and measure it before manufacturing
STLs. Do not claim that an arbitrary card fits the production enclosure.
The source includes render-time clearance assertions for the Pi, capture-card,
AtomS3U, and cradle rails so a larger production-SKU override fails visibly
instead of silently producing intersecting solids.

Recommended material is PETG or a higher-temperature printable material. The
lid and both long walls are ventilated. Keep the Pi heatsinks below the exhaust
field, expose the AtomS3U arm/reset button and LED, add strain relief to every
panel extension, and validate temperatures during a sustained 1080p30 encode.

Using certified Pi and M5Stack modules helps component compliance; it does not
automatically certify the assembled retail product. Complete the applicable
EMC, safety, labeling, radio-module integration, materials, and regional review
before sale.
