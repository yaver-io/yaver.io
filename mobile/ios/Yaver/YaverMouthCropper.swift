import AVFoundation
import CoreImage
import Foundation
import React
import Vision

/// Converts a short, device-local front-camera recording into normalized
/// grayscale mouth ROIs. The source video is never returned across the bridge;
/// JS deletes its temporary file immediately after this call completes.
@objc(YaverMouthCropper)
final class YaverMouthCropper: NSObject, RCTBridgeModule {
  static func moduleName() -> String! { "YaverMouthCropper" }
  static func requiresMainQueueSetup() -> Bool { false }

  private let queue = DispatchQueue(label: "io.yaver.mouth-cropper", qos: .userInitiated)
  private let context = CIContext(options: [.cacheIntermediates: false])

  @objc func processVideo(
    _ path: String,
    options: NSDictionary,
    resolver resolve: @escaping RCTPromiseResolveBlock,
    rejecter reject: @escaping RCTPromiseRejectBlock
  ) {
    queue.async {
      do {
        let started = Date()
        let width = min(max(options["width"] as? Int ?? 96, 64), 160)
        let height = min(max(options["height"] as? Int ?? 96, 64), 160)
        let fps = min(max(options["fps"] as? Int ?? 25, 10), 30)
        let maxDuration = min(max(options["maxDurationMs"] as? Int ?? 8_000, 500), 10_000)
        let frames = try self.extract(path: path, width: width, height: height, fps: fps, maxDurationMs: maxDuration)
        resolve(["frames": frames, "durationMs": Int(Date().timeIntervalSince(started) * 1000)])
      } catch {
        reject("MOUTH_CROP_FAILED", error.localizedDescription, error)
      }
    }
  }

  private func extract(path: String, width: Int, height: Int, fps: Int, maxDurationMs: Int) throws -> [[String: Any]] {
    let cleanPath = path.hasPrefix("file://") ? String(path.dropFirst(7)) : path
    let asset = AVURLAsset(url: URL(fileURLWithPath: cleanPath))
    let durationSeconds = min(CMTimeGetSeconds(asset.duration), Double(maxDurationMs) / 1000.0)
    guard durationSeconds.isFinite, durationSeconds > 0 else { throw CropError.invalidVideo }

    let generator = AVAssetImageGenerator(asset: asset)
    generator.appliesPreferredTrackTransform = true
    generator.requestedTimeToleranceBefore = CMTime(value: 1, timescale: CMTimeScale(fps * 2))
    generator.requestedTimeToleranceAfter = generator.requestedTimeToleranceBefore

    let count = min(Int(floor(durationSeconds * Double(fps))), fps * 10)
    var output: [[String: Any]] = []
    var stable: CGRect?
    for index in 0..<count {
      autoreleasepool {
        let seconds = Double(index) / Double(fps)
        let time = CMTime(seconds: seconds, preferredTimescale: 600)
        guard let image = try? generator.copyCGImage(at: time, actualTime: nil),
              let rawMouth = self.mouthRect(in: image) else { return }
        let mouth = self.stabilized(rawMouth, previous: stable)
        stable = mouth
        guard let bytes = self.grayCrop(image: image, rect: mouth, width: width, height: height) else { return }
        output.append([
          "timestamp": Int(seconds * 1000),
          "width": width,
          "height": height,
          "format": "gray8",
          "data": bytes.base64EncodedString(),
        ])
      }
    }
    return output
  }

  private func mouthRect(in image: CGImage) -> CGRect? {
    let request = VNDetectFaceLandmarksRequest()
    let handler = VNImageRequestHandler(cgImage: image, orientation: .up)
    try? handler.perform([request])
    guard let face = (request.results as? [VNFaceObservation])?.max(by: { $0.boundingBox.width < $1.boundingBox.width }),
          let lips = face.landmarks?.outerLips,
          lips.pointCount >= 4 else { return nil }

    let imageWidth = CGFloat(image.width)
    let imageHeight = CGFloat(image.height)
    let points = lips.normalizedPoints.map { point -> CGPoint in
      let x = (face.boundingBox.minX + point.x * face.boundingBox.width) * imageWidth
      let visionY = face.boundingBox.minY + point.y * face.boundingBox.height
      return CGPoint(x: x, y: (1 - visionY) * imageHeight)
    }
    let minX = points.map(\.x).min()!
    let maxX = points.map(\.x).max()!
    let minY = points.map(\.y).min()!
    let maxY = points.map(\.y).max()!
    let center = CGPoint(x: (minX + maxX) / 2, y: (minY + maxY) / 2)
    let side = max(maxX - minX, maxY - minY) * 2.15
    return CGRect(x: center.x - side / 2, y: center.y - side / 2, width: side, height: side)
      .intersection(CGRect(x: 0, y: 0, width: imageWidth, height: imageHeight))
  }

  private func stabilized(_ current: CGRect, previous: CGRect?) -> CGRect {
    guard let previous else { return current }
    let alpha: CGFloat = 0.35
    return CGRect(
      x: previous.origin.x * (1 - alpha) + current.origin.x * alpha,
      y: previous.origin.y * (1 - alpha) + current.origin.y * alpha,
      width: previous.width * (1 - alpha) + current.width * alpha,
      height: previous.height * (1 - alpha) + current.height * alpha
    )
  }

  private func grayCrop(image: CGImage, rect: CGRect, width: Int, height: Int) -> Data? {
    guard rect.width > 4, rect.height > 4, let cropped = image.cropping(to: rect.integral) else { return nil }
    var bytes = [UInt8](repeating: 0, count: width * height)
    guard let bitmap = CGContext(
      data: &bytes,
      width: width,
      height: height,
      bitsPerComponent: 8,
      bytesPerRow: width,
      space: CGColorSpaceCreateDeviceGray(),
      bitmapInfo: CGImageAlphaInfo.none.rawValue
    ) else { return nil }
    bitmap.interpolationQuality = .high
    bitmap.draw(cropped, in: CGRect(x: 0, y: 0, width: width, height: height))
    return Data(bytes)
  }
}

private enum CropError: LocalizedError {
  case invalidVideo
  var errorDescription: String? { "The temporary camera recording could not be read." }
}
