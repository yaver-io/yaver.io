import Foundation
import ReplayKit
import AVFoundation
import React

/// Native screen recorder using ReplayKit's in-app capture API.
/// Writes H264+AAC MP4 to a temp file. Module name matches Android's
/// ScreenRecorder so the JS bridge works identically on both platforms.
@objc(ScreenRecorder)
class YaverScreenRecorder: NSObject, RCTBridgeModule {

    static func moduleName() -> String! { return "ScreenRecorder" }
    static func requiresMainQueueSetup() -> Bool { return false }

    private var assetWriter: AVAssetWriter?
    private var videoInput: AVAssetWriterInput?
    private var audioInput: AVAssetWriterInput?
    private var outputPath: String?
    private var isRecording = false
    private var sessionStarted = false

    @objc func startRecording(_ resolve: @escaping RCTPromiseResolveBlock,
                              rejecter reject: @escaping RCTPromiseRejectBlock) {
        if isRecording {
            reject("ALREADY_RECORDING", "Already recording", nil)
            return
        }

        let recorder = RPScreenRecorder.shared()
        guard recorder.isAvailable else {
            reject("UNAVAILABLE", "Screen recording is not available", nil)
            return
        }

        // Prepare output file.
        let timestamp = Int(Date().timeIntervalSince1970)
        let path = NSTemporaryDirectory() + "mobile-screen-\(timestamp).mp4"
        outputPath = path

        do {
            let url = URL(fileURLWithPath: path)
            assetWriter = try AVAssetWriter(url: url, fileType: .mp4)
        } catch {
            reject("WRITER_FAILED", error.localizedDescription, error)
            return
        }

        // Video input — H264, screen-sized.
        let videoSettings: [String: Any] = [
            AVVideoCodecKey: AVVideoCodecType.h264,
            AVVideoWidthKey: UIScreen.main.bounds.width * UIScreen.main.scale,
            AVVideoHeightKey: UIScreen.main.bounds.height * UIScreen.main.scale,
        ]
        videoInput = AVAssetWriterInput(mediaType: .video, outputSettings: videoSettings)
        videoInput?.expectsMediaDataInRealTime = true
        if let vi = videoInput { assetWriter?.add(vi) }

        // Audio input — AAC.
        let audioSettings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVNumberOfChannelsKey: 1,
            AVSampleRateKey: 44100,
            AVEncoderBitRateKey: 64000,
        ]
        audioInput = AVAssetWriterInput(mediaType: .audio, outputSettings: audioSettings)
        audioInput?.expectsMediaDataInRealTime = true
        if let ai = audioInput { assetWriter?.add(ai) }

        sessionStarted = false
        isRecording = true

        recorder.startCapture(handler: { [weak self] sampleBuffer, bufferType, error in
            guard let self = self, self.isRecording, error == nil else { return }
            guard let writer = self.assetWriter, writer.status == .writing || !self.sessionStarted else { return }

            if !self.sessionStarted {
                writer.startWriting()
                writer.startSession(atSourceTime: CMSampleBufferGetPresentationTimeStamp(sampleBuffer))
                self.sessionStarted = true
            }

            switch bufferType {
            case .video:
                if let vi = self.videoInput, vi.isReadyForMoreMediaData {
                    vi.append(sampleBuffer)
                }
            case .audioApp, .audioMic:
                if let ai = self.audioInput, ai.isReadyForMoreMediaData {
                    ai.append(sampleBuffer)
                }
            @unknown default:
                break
            }
        }, completionHandler: { error in
            if let error = error {
                reject("CAPTURE_FAILED", error.localizedDescription, error)
                self.isRecording = false
            } else {
                resolve(true)
            }
        })
    }

    @objc func stopRecording(_ resolve: @escaping RCTPromiseResolveBlock,
                             rejecter reject: @escaping RCTPromiseRejectBlock) {
        guard isRecording else {
            reject("NOT_RECORDING", "Not currently recording", nil)
            return
        }

        isRecording = false
        RPScreenRecorder.shared().stopCapture { [weak self] error in
            guard let self = self else { return }
            if let error = error {
                reject("STOP_FAILED", error.localizedDescription, error)
                return
            }
            self.videoInput?.markAsFinished()
            self.audioInput?.markAsFinished()
            self.assetWriter?.finishWriting {
                resolve(self.outputPath)
                self.assetWriter = nil
                self.videoInput = nil
                self.audioInput = nil
            }
        }
    }

    @objc func isRecordingActive(_ resolve: @escaping RCTPromiseResolveBlock,
                                 rejecter reject: @escaping RCTPromiseRejectBlock) {
        resolve(isRecording)
    }
}
