import Foundation
import AVFoundation
import React

/// TTS + mic-recording bridge for tvOS.
///
/// Apple does not ship on-device speech recognition (Speech.framework) on tvOS,
/// so STT here = record the system microphone (Siri Remote / MacBook on the
/// simulator) to an audio file; JS then uploads it to a transcription backend.
/// TTS uses AVSpeechSynthesizer, which IS available on tvOS.
@objc(YaverSpeech)
class YaverSpeech: RCTEventEmitter {

  private var speechSynthesizer = AVSpeechSynthesizer()
  private var audioEngine = AVAudioEngine()
  private var audioFile: AVAudioFile?
  private var recordUrl: URL?
  private var isRecording = false
  private var hasInputTap = false

  private func releaseRecordingSession() {
    let session = AVAudioSession.sharedInstance()
    try? session.setActive(false, options: [.notifyOthersOnDeactivation])
    try? session.setCategory(.ambient, mode: .default, options: [.mixWithOthers])
  }

  override static func moduleName() -> String! {
    return "YaverSpeech"
  }

  override static func requiresMainQueueSetup() -> Bool {
    return false
  }

  override func supportedEvents() -> [String]! {
    return ["onError"]
  }

  // ── TTS ────────────────────────────────────────────────────────────

  @objc func speak(_ text: String, rate: NSNumber, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    let utterance = AVSpeechUtterance(string: text)
    utterance.rate = rate.doubleValue > 0 ? rate.floatValue : AVSpeechUtteranceDefaultSpeechRate
    utterance.voice = AVSpeechSynthesisVoice(language: "en-US")
    speechSynthesizer.speak(utterance)
    resolve(true)
  }

  @objc func stopSpeaking(_ resolve: RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    speechSynthesizer.stopSpeaking(at: .immediate)
    resolve(true)
  }

  @objc func isTtsSpeaking(_ resolve: RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    resolve(speechSynthesizer.isSpeaking)
  }

  // ── Mic recording (feeds a transcription backend) ─────────────────

  @objc func startListening(_ locale: NSString?, resolve: @escaping RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    guard !isRecording else {
      resolve(true)
      return
    }
    let session = AVAudioSession.sharedInstance()
    do {
      try session.setCategory(.record, mode: .measurement, options: [.duckOthers])
      try session.setActive(true, options: [])
    } catch {
      releaseRecordingSession()
      reject("audio", "Failed to start audio session: \(error.localizedDescription)", error)
      return
    }

    let url = FileManager.default.temporaryDirectory
      .appendingPathComponent("yaver-voice-\(UUID().uuidString).wav")

    do {
      let inputNode = audioEngine.inputNode
      let format = inputNode.outputFormat(forBus: 0)
      let file = try AVAudioFile(forWriting: url, settings: format.settings)
      inputNode.installTap(onBus: 0, bufferSize: 4096, format: format) { [weak self] buffer, _ in
        try? self?.audioFile?.write(from: buffer)
      }
      hasInputTap = true
      audioEngine.prepare()
      try audioEngine.start()
      self.audioFile = file
      self.recordUrl = url
      self.isRecording = true
      resolve(true)
    } catch {
      if audioEngine.isRunning { audioEngine.stop() }
      if hasInputTap {
        audioEngine.inputNode.removeTap(onBus: 0)
        hasInputTap = false
      }
      audioFile = nil
      recordUrl = nil
      isRecording = false
      releaseRecordingSession()
      reject("record", "Failed to start recording: \(error.localizedDescription)", error)
    }
  }

  /// Stops recording and resolves the absolute path of the recorded audio file.
  @objc func stopListening(_ resolve: RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    if audioEngine.isRunning {
      audioEngine.stop()
    }
    if hasInputTap {
      audioEngine.inputNode.removeTap(onBus: 0)
      hasInputTap = false
    }
    audioFile = nil
    isRecording = false
    releaseRecordingSession()
    if let url = recordUrl, FileManager.default.fileExists(atPath: url.path) {
      recordUrl = nil
      resolve(url.path)
      return
    }
    recordUrl = nil
    reject("no_recording", "No recording available", nil)
  }

  @objc func isRecordingAudio(_ resolve: RCTPromiseResolveBlock, reject: @escaping RCTPromiseRejectBlock) {
    resolve(isRecording)
  }
}
