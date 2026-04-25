# yaver-cagent — Yocto recipe skeleton.
#
# Drop into a layer at e.g.
# meta-yaver/recipes-extended/yaver-cagent/yaver-cagent_0.0.1.bb,
# add `BBLAYERS += "/path/to/meta-yaver"` to your bblayers.conf,
# then bitbake yaver-cagent.

SUMMARY = "Yaver c-agent device-side runtime"
DESCRIPTION = "Hosts signed code modules shipped from a cloud reasoning \
brain for AI-driven IoT troubleshooting."
HOMEPAGE = "https://github.com/kivanccakmak/yaver.io"
LICENSE = "CLOSED"

# Replace with the real source path / SRCREV for your release.
SRC_URI = "git://github.com/kivanccakmak/yaver.io;protocol=https;branch=main"
SRCREV  = "${AUTOREV}"

S = "${WORKDIR}/git/embedded/c-agent"

inherit cmake pkgconfig

EXTRA_OECMAKE = " \
    -DYVR_CAGENT_TESTS=OFF \
    -DCMAKE_BUILD_TYPE=Release \
"

FILES:${PN} += " \
    ${bindir}/yaver-cagent-config \
    ${libdir}/libyvr_cagent_core.a \
    ${libdir}/libyvr_cagent_host.a \
    ${libdir}/pkgconfig/yaver-cagent.pc \
"

FILES:${PN}-dev += " \
    ${includedir}/yvr/*.h \
    ${libdir}/cmake/yaver_cagent/* \
"
