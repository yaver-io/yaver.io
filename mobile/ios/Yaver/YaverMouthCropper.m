#import <React/RCTBridgeModule.h>

@interface RCT_EXTERN_MODULE(YaverMouthCropper, NSObject)

RCT_EXTERN_METHOD(processVideo:(NSString *)path
                  options:(NSDictionary *)options
                  resolver:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

@end
