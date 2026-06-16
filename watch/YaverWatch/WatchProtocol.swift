// WatchProtocol.swift — the v1 wire protocol between the watch and the phone
// (over WCSession) and, in standalone mode, between the watch and the agent
// (over HTTP POST /watch/turn). This file is the SINGLE SOURCE OF TRUTH for the
// JSON keys; nothing else hard-codes a kind string or a field name.
//
// Design invariants (docs/yaver-smartwatch-voice-terminal.md §3/§4):
//   • The watch SENDS a transcript / confirm / intent and RECEIVES a single
//     short spoken sentence (+ optional taskId / confirm token). It never
//     receives code, diffs, or structured screen content.
//   • Every write/deploy verb is confirm-gated. The watch does NOT decide what
//     needs confirmation — the PHONE (or agent) replies `confirmNeeded` with a
//     token + prompt, and the watch echoes back a `confirm` message.
//
// Transport framing:
//   • WCSession: both directions carry one dict with key "yaverWatch" whose
//     value is the JSON string of the message below.
//   • HTTP standalone: the request body IS the JSON of a WatchRequest; the
//     response body IS the JSON of a WatchReply.

import Foundation

/// The dictionary key under which the JSON-encoded message travels in a
/// WCSession message / reply payload. Pinned — must match the Android/phone side.
enum WatchWire {
    static let key = "yaverWatch"
    static let version = 1
}

// MARK: - Watch → Phone / Agent

/// The `kind` discriminator for messages the watch sends.
enum WatchRequestKind: String, Codable {
    case transcript   // a spoken command
    case confirm      // answer to a confirmNeeded prompt
    case intent       // a fixed complication quick-action
}

/// Reply to a confirm prompt.
enum ConfirmReply: String, Codable {
    case confirm
    case cancel
}

/// Fixed complication intents (the cheapest interaction: no speaking).
enum WatchIntent: String, Codable, CaseIterable {
    case runTests = "run-tests"
    case deploy
    case status
}

/// Watch → Phone (WCSession) / Watch → Agent (POST /watch/turn body).
///
/// JSON shapes (pinned exactly):
///   {"v":1,"kind":"transcript","text":"<spoken command>"}
///   {"v":1,"kind":"confirm","token":"<token>","reply":"confirm"|"cancel"}
///   {"v":1,"kind":"intent","intent":"run-tests"|"deploy"|"status"}
struct WatchRequest: Codable {
    var v: Int = WatchWire.version
    var kind: WatchRequestKind
    var text: String?           // transcript
    var token: String?          // confirm
    var reply: ConfirmReply?    // confirm
    var intent: WatchIntent?    // intent

    enum CodingKeys: String, CodingKey { case v, kind, text, token, reply, intent }

    static func transcript(_ text: String) -> WatchRequest {
        WatchRequest(kind: .transcript, text: text)
    }
    static func confirm(token: String, reply: ConfirmReply) -> WatchRequest {
        WatchRequest(kind: .confirm, token: token, reply: reply)
    }
    static func intent(_ intent: WatchIntent) -> WatchRequest {
        WatchRequest(kind: .intent, intent: intent)
    }
}

// MARK: - Phone / Agent → Watch

/// The `kind` discriminator for messages the watch receives. The wire string
/// for `confirmNeeded` is the hyphenated "confirm-needed"; the only place that
/// mapping lives is `WatchReply.wire(for:)` / `WatchReply.kind(from:)` below,
/// so this enum is decoded/encoded manually (not via Codable synthesis).
enum WatchReplyKind {
    case ack            // accepted; "On it."
    case confirmNeeded  // needs a confirm/cancel before proceeding
    case working        // long task started; will wake the wrist on completion
    case summary        // terminal result, one sentence
    case error          // couldn't do it
    case handoff        // sent to a bigger screen (phone)
}

/// Phone → Watch (WCSession reply) / Agent → Watch (POST /watch/turn response).
///
/// JSON shapes (pinned exactly):
///   {"v":1,"kind":"ack","spoken":"On it."}
///   {"v":1,"kind":"confirm-needed","token":"<token>","prompt":"That looks like a deploy command — confirm?"}
///   {"v":1,"kind":"working","taskId":"<id>","spoken":"Working…"}
///   {"v":1,"kind":"summary","taskId":"<id>","status":"completed","spoken":"Done. Tests pass."}
///   {"v":1,"kind":"error","spoken":"I couldn't reach your box."}
///   {"v":1,"kind":"handoff","target":"phone","spoken":"Sent it to your phone."}
struct WatchReply: Codable {
    var v: Int = WatchWire.version
    var kind: WatchReplyKind
    var spoken: String?     // the one sentence to show + speak
    var token: String?      // confirmNeeded
    var prompt: String?     // confirmNeeded — the question to show on the wrist
    var taskId: String?     // working / summary
    var status: String?     // summary — "completed" | "failed" | "review" | …
    var target: String?     // handoff — e.g. "phone"

    // The `kind` raw values use a custom mapping for confirm-needed's hyphen,
    // so decode/encode kind via its rawValue string explicitly.
    enum CodingKeys: String, CodingKey { case v, kind, spoken, token, prompt, taskId, status, target }

    init(kind: WatchReplyKind, spoken: String? = nil, token: String? = nil,
         prompt: String? = nil, taskId: String? = nil, status: String? = nil, target: String? = nil) {
        self.kind = kind
        self.spoken = spoken
        self.token = token
        self.prompt = prompt
        self.taskId = taskId
        self.status = status
        self.target = target
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        v = (try? c.decode(Int.self, forKey: .v)) ?? WatchWire.version
        let rawKind = try c.decode(String.self, forKey: .kind)
        guard let k = WatchReply.kind(from: rawKind) else {
            throw DecodingError.dataCorruptedError(forKey: .kind, in: c, debugDescription: "unknown kind \(rawKind)")
        }
        kind = k
        spoken = try? c.decode(String.self, forKey: .spoken)
        token = try? c.decode(String.self, forKey: .token)
        prompt = try? c.decode(String.self, forKey: .prompt)
        taskId = try? c.decode(String.self, forKey: .taskId)
        status = try? c.decode(String.self, forKey: .status)
        target = try? c.decode(String.self, forKey: .target)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(v, forKey: .v)
        try c.encode(WatchReply.wire(for: kind), forKey: .kind)
        try c.encodeIfPresent(spoken, forKey: .spoken)
        try c.encodeIfPresent(token, forKey: .token)
        try c.encodeIfPresent(prompt, forKey: .prompt)
        try c.encodeIfPresent(taskId, forKey: .taskId)
        try c.encodeIfPresent(status, forKey: .status)
        try c.encodeIfPresent(target, forKey: .target)
    }

    /// Wire string for a kind (the only place the "confirm-needed" hyphen lives).
    static func wire(for kind: WatchReplyKind) -> String {
        switch kind {
        case .ack: return "ack"
        case .confirmNeeded: return "confirm-needed"
        case .working: return "working"
        case .summary: return "summary"
        case .error: return "error"
        case .handoff: return "handoff"
        }
    }

    static func kind(from wire: String) -> WatchReplyKind? {
        switch wire {
        case "ack": return .ack
        case "confirm-needed": return .confirmNeeded
        case "working": return .working
        case "summary": return .summary
        case "error": return .error
        case "handoff": return .handoff
        default: return nil
        }
    }
}

// MARK: - JSON string (de)serialization for the WCSession "yaverWatch" envelope

enum WatchCodec {
    private static let encoder = JSONEncoder()
    private static let decoder = JSONDecoder()

    /// Encode a request to the JSON string carried under WatchWire.key.
    static func encode(_ req: WatchRequest) throws -> String {
        let data = try encoder.encode(req)
        return String(decoding: data, as: UTF8.self)
    }

    /// Decode a reply from the JSON string carried under WatchWire.key.
    static func decodeReply(_ json: String) throws -> WatchReply {
        try decoder.decode(WatchReply.self, from: Data(json.utf8))
    }

    /// Decode a reply directly from HTTP response bytes (standalone mode).
    static func decodeReply(_ data: Data) throws -> WatchReply {
        try decoder.decode(WatchReply.self, from: data)
    }

    /// Encode a request to HTTP body bytes (standalone mode).
    static func encodeData(_ req: WatchRequest) throws -> Data {
        try encoder.encode(req)
    }

    /// Wrap a request JSON string in the WCSession dictionary envelope.
    static func envelope(_ req: WatchRequest) throws -> [String: Any] {
        [WatchWire.key: try encode(req)]
    }

    /// Pull a reply out of a WCSession reply dictionary.
    static func reply(from dict: [String: Any]) throws -> WatchReply {
        guard let json = dict[WatchWire.key] as? String else {
            throw WatchProtocolError.malformed
        }
        return try decodeReply(json)
    }
}

enum WatchProtocolError: Error, LocalizedError {
    case malformed
    var errorDescription: String? { "Malformed message from phone." }
}
