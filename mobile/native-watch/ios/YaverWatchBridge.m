// YaverWatchBridge.m — ObjC bridge declaration so React Native can see the
// Swift YaverWatchBridge (RCTEventEmitter). Mirrors the native-mesh bridge.
// See YaverWatchBridge.swift + src/lib/watchEntry.ts.

#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>

@interface RCT_EXTERN_MODULE(YaverWatchBridge, RCTEventEmitter)

// JS → native: ship a reply JSON string back to the watch (transferUserInfo).
RCT_EXTERN_METHOD(sendToWatch:(NSString *)json)

@end
