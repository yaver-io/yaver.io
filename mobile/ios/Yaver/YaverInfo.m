#import <React/RCTBridgeModule.h>

@interface RCT_EXTERN_MODULE(YaverInfo, NSObject)
RCT_EXTERN_METHOD(getLastGuestCrashReport:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
RCT_EXTERN_METHOD(clearLastGuestCrashReport:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
RCT_EXTERN_METHOD(consumePendingFeedbackLaunch:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
RCT_EXTERN_METHOD(consumePendingCarVoiceLaunch:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)
RCT_EXTERN_METHOD(setInheritedAuth:(NSString *)token
                  agentUrl:(NSString *)agentUrl
                  deviceId:(NSString *)deviceId)
RCT_EXTERN_METHOD(setInheritedRelayPassword:(NSString *)password)
RCT_EXTERN_METHOD(setInheritedGuestProject:(NSString *)name
                  path:(NSString *)path)
RCT_EXTERN_METHOD(setInheritedPrimaryRunner:(NSString *)runner
                  model:(NSString *)model)
RCT_EXTERN_METHOD(setCarPlayVoiceState:(NSString *)state)
RCT_EXTERN_METHOD(clearInheritedAuth)
@end
