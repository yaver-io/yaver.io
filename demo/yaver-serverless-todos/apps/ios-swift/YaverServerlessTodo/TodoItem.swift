import Foundation

struct TodoItem: Identifiable, Codable, Equatable {
    let id: String
    let title: String
    let done: Bool
    let ownerId: String?
    let createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case text
        case done
        case ownerId = "owner_id"
        case createdAt = "created_at"
    }

    init(id: String, title: String, done: Bool, ownerId: String? = nil, createdAt: String? = nil) {
        self.id = id
        self.title = title
        self.done = done
        self.ownerId = ownerId
        self.createdAt = createdAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        title = try container.decodeIfPresent(String.self, forKey: .title)
            ?? container.decodeIfPresent(String.self, forKey: .text)
            ?? ""
        if let bool = try? container.decode(Bool.self, forKey: .done) {
            done = bool
        } else if let int = try? container.decode(Int.self, forKey: .done) {
            done = int != 0
        } else if let string = try? container.decode(String.self, forKey: .done) {
            done = string == "1" || string.lowercased() == "true"
        } else {
            done = false
        }
        ownerId = try container.decodeIfPresent(String.self, forKey: .ownerId)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
    }
}
