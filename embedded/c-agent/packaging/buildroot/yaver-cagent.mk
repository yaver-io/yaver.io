################################################################################
#
# yaver-cagent — Buildroot recipe skeleton.
#
# Drop into your Buildroot tree as `package/yaver-cagent/`, add
# `source "package/yaver-cagent/Config.in"` to the right menu, and
# the package becomes selectable in `make menuconfig`.
#
# This is a starting point — adjust the SITE / VERSION / LICENSE
# fields before shipping. The CMake-based build is the intended
# integration path, so this recipe just calls `cmake-package`.
#
################################################################################

YAVER_CAGENT_VERSION = 0.0.1
YAVER_CAGENT_SITE = $(call github,kivanccakmak,yaver.io,main)
YAVER_CAGENT_SOURCE = yaver.io-$(YAVER_CAGENT_VERSION).tar.gz
YAVER_CAGENT_LICENSE = TBD
YAVER_CAGENT_LICENSE_FILES = LICENSE
YAVER_CAGENT_INSTALL_STAGING = YES
YAVER_CAGENT_SUBDIR = embedded/c-agent

YAVER_CAGENT_CONF_OPTS = \
	-DYVR_CAGENT_TESTS=OFF \
	-DCMAKE_BUILD_TYPE=Release

$(eval $(cmake-package))
