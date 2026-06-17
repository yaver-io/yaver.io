#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>

@interface RCT_EXTERN_MODULE(YaverWatchBridge, RCTEventEmitter)

RCT_EXTERN_METHOD(sendToWatch:(NSString *)json)

@end
