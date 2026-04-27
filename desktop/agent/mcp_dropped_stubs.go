package main

// mcp_dropped_stubs.go — the IoT / lifestyle / dailydev / premium MCP
// families were dropped 2026-04-28 (project_lean_stack_2026_04_28.md).
// A concurrent thread keeps restoring `case "ha_states":` etc. blocks
// in httpserver.go that call these handlers. Rather than fight every
// restoration, this file ships variadic-arg no-op stubs so the build
// stays green regardless of how many times the case-blocks come back.
//
// Each stub returns a "feature_removed" map so any caller that hits
// one sees a clear, structured signal instead of the runtime fault
// they'd get from a missing function.
//
// Do not extend these stubs. If a feature genuinely belongs back in
// the lean stack, restore the original family file end-to-end with
// the user's go-ahead.

func droppedMCPStub(_ ...interface{}) interface{} {
	return map[string]interface{}{
		"error":  "feature_removed",
		"detail": "this MCP tool was removed in the 2026-04-28 lean-stack cut",
	}
}

func mcpADBCommand(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpADBDevices(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpADBScreenshot(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpBattery(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpBrightness(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpCalculate(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpConvertUnits(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpCountdown(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpCronExplain(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpCryptoPrice(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpCurrencyExchange(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpDesktopNotify(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpDirections(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpDiskUsage(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpDomainCheck(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpEVCharging(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpEVConnectorTypes(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpEVNetworks(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpEczaneSearch(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpElgatoControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpElgatoStatus(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpEpoch(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpFakeData(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpGeocode(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpGitHubTrending(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpGoveeControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpGoveeDevices(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHACall(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHAService(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHAStates(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHAToggle(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHTTPStatusLookup(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHotels(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHueActivateScene(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHueControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHueLights(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpHueScenes(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpIPGeo(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpJWTDecode(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpListShortcuts(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpMQTTPublish(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpMusicControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpNPMInfo(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpNanoleafControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpNews(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpNobetciEczane(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpNobetciEczaneFallback(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpOpenURL(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpPasswordGen(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpPlacesSearch(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpPublicIP(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpQRCode(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpRestaurants(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpRunShortcut(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSay(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpScreenLock(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpShellyControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpShellyPower(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpShellyStatus(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSiteCheck(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSonosControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSonosDiscover(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSpeedTest(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpStockPrice(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpSubnet(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpTasmotaControl(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpTimer(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpTranslate(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpUptime(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpVolume(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpWakeOnLAN(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpWeather(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpWhois(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpWiFiInfo(args ...interface{}) interface{} { return droppedMCPStub(args...) }
func mcpWorldClock(args ...interface{}) interface{} { return droppedMCPStub(args...) }
