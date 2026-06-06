// YaverMeshModule.m — ObjC bridge declarations exposing the Swift YaverMesh
// module to React Native. REFERENCE; added to the app target by
// mobile/plugins/withMeshTunnel.js. See YaverMeshModule.swift.

#import <React/RCTBridgeModule.h>

@interface RCT_EXTERN_MODULE(YaverMesh, NSObject)

RCT_EXTERN_METHOD(ensureKeyPair:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(up:(NSString *)wgQuickConfig
                  resolver:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(reconfigure:(NSString *)wgQuickConfig
                  resolver:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(down:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(status:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

@end
