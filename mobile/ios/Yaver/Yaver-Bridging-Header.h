//
// Use this file to import your target's public headers that you would like to expose to Swift.
//

#if __has_include(<GCDWebServer/GCDWebServer.h>)
#import <GCDWebServer/GCDWebServer.h>
#import <GCDWebServer/GCDWebServerDataRequest.h>
#import <GCDWebServer/GCDWebServerDataResponse.h>
#elif __has_include("GCDWebServer.h")
#import "GCDWebServer.h"
#import "GCDWebServerDataRequest.h"
#import "GCDWebServerDataResponse.h"
#endif
