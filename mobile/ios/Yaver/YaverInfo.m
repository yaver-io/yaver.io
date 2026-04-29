#import <React/RCTBridgeModule.h>

@interface RCT_EXTERN_MODULE(YaverInfo, NSObject)
RCT_EXTERN_METHOD(getLastGuestCrashReport:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
RCT_EXTERN_METHOD(clearLastGuestCrashReport:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
@end
