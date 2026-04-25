# yaver-cagent — OpenWrt feed Makefile skeleton.
#
# Drop into a feed at e.g. `feeds/yaver/yaver-cagent/Makefile`, run
# `./scripts/feeds update -a && ./scripts/feeds install yaver-cagent`,
# then enable in `make menuconfig`.

include $(TOPDIR)/rules.mk

PKG_NAME:=yaver-cagent
PKG_VERSION:=0.0.1
PKG_RELEASE:=1
PKG_LICENSE:=TBD
PKG_BUILD_PARALLEL:=1
PKG_BUILD_DEPENDS:=

# Replace with the actual source URL + checksum for your release.
PKG_SOURCE:=$(PKG_NAME)-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://github.com/kivanccakmak/yaver.io/releases/download/v$(PKG_VERSION)
PKG_HASH:=skip

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/cmake.mk

define Package/yaver-cagent
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Yaver c-agent device-side runtime
  URL:=https://github.com/kivanccakmak/yaver.io
endef

define Package/yaver-cagent/description
  Device-side runtime for Yaver's AI-driven IoT troubleshooting.
  Hosts signed code modules shipped from a cloud reasoning brain
  over the existing Yaver control plane.
endef

CMAKE_OPTIONS += \
	-DYVR_CAGENT_TESTS=OFF \
	-DCMAKE_BUILD_TYPE=Release

CMAKE_SOURCE_SUBDIR:=embedded/c-agent

define Package/yaver-cagent/install
	$(INSTALL_DIR) $(1)/usr/lib
	$(CP) $(PKG_INSTALL_DIR)/usr/lib/libyvr_cagent_core.a $(1)/usr/lib/
	$(CP) $(PKG_INSTALL_DIR)/usr/lib/libyvr_cagent_host.a $(1)/usr/lib/
	$(INSTALL_DIR) $(1)/usr/include/yvr
	$(CP) $(PKG_INSTALL_DIR)/usr/include/yvr/*.h $(1)/usr/include/yvr/
	$(INSTALL_DIR) $(1)/usr/lib/pkgconfig
	$(CP) $(PKG_INSTALL_DIR)/usr/lib/pkgconfig/yaver-cagent.pc $(1)/usr/lib/pkgconfig/
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_INSTALL_DIR)/usr/bin/yaver-cagent-config $(1)/usr/bin/
endef

$(eval $(call BuildPackage,yaver-cagent))
