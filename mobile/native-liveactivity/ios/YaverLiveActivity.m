#import <React/RCTBridgeModule.h>

// Bridges NativeModules.YaverLiveActivity → YaverLiveActivity.swift.
// Selectors must match the @objc(...) signatures there exactly.
@interface RCT_EXTERN_MODULE (YaverLiveActivity, NSObject)

RCT_EXTERN_METHOD(start
                  : (NSString *)machine taskId
                  : (NSString *)taskId status
                  : (NSString *)status headline
                  : (NSString *)headline detail
                  : (NSString *)detail progress
                  : (nullable NSNumber *)progress resolver
                  : (RCTPromiseResolveBlock)resolve rejecter
                  : (RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(update
                  : (NSString *)status headline
                  : (NSString *)headline detail
                  : (NSString *)detail progress
                  : (nullable NSNumber *)progress resolver
                  : (RCTPromiseResolveBlock)resolve rejecter
                  : (RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(end
                  : (NSString *)status headline
                  : (NSString *)headline detail
                  : (NSString *)detail dismissAfter
                  : (nullable NSNumber *)dismissAfter resolver
                  : (RCTPromiseResolveBlock)resolve rejecter
                  : (RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(isAvailable
                  : (RCTPromiseResolveBlock)resolve rejecter
                  : (RCTPromiseRejectBlock)reject)

@end
