import SwiftUI
#if !os(watchOS)
import CoreImage
import CoreImage.CIFilterBuiltins
#endif

struct RemoteApprovalView:View {
 let output:String
 private var url:URL? {
  output.components(separatedBy:.whitespacesAndNewlines).compactMap(URL.init(string:)).first{u in u.scheme=="https" && ["yaver.io","www.yaver.io"].contains(u.host ?? "") && u.path.hasPrefix("/auth/")}
 }
 var body:some View {
  if let url {
   #if os(tvOS)
   if let image=qr(url.absoluteString){Image(decorative:image,scale:1).interpolation(.none).resizable().frame(width:220,height:220).padding(8).background(.white)}
   Text("Scan to approve this remote in Yaver").font(.caption)
   #elseif !os(watchOS)
   Link("Approve remote sign-in",destination:url)
   #else
   Text("Approve the code below in Yaver on your phone.").font(.caption)
   #endif
  }
 }
 #if !os(watchOS)
 private func qr(_ value:String)->CGImage?{let filter=CIFilter.qrCodeGenerator();filter.message=Data(value.utf8);guard let image=filter.outputImage else{return nil};return CIContext().createCGImage(image,from:image.extent)}
 #endif
}
