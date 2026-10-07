#import <React/RCTBridgeModule.h>
#import <PlainSSH/PlainSSH.h>

@interface YaverPlainSSH : NSObject <RCTBridgeModule>
@end

@implementation YaverPlainSSH
RCT_EXPORT_MODULE();
+ (BOOL)requiresMainQueueSetup { return NO; }
- (void)invalidate { PlainsshInvoke(@"{\"op\":\"closeAll\"}"); }
RCT_EXPORT_METHOD(invoke:(NSString *)request
                  resolve:(RCTPromiseResolveBlock)resolve
                  reject:(RCTPromiseRejectBlock)reject) {
  // Reads wait at most one second. Never block the UI or serialize stdin
  // behind a pending read; the Go core owns connection synchronization.
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
    resolve(PlainsshInvoke(request));
  });
}
@end
