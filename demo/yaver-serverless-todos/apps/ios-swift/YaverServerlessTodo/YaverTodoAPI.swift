import Foundation

struct TodoListResponse: Decodable {
    let rows: [TodoItem]
}

final class YaverTodoAPI {
    var baseUrl: String
    var slug: String
    var token: String

    init(baseUrl: String, slug: String, token: String) {
        self.baseUrl = baseUrl
        self.slug = slug
        self.token = token
    }

    func list() async throws -> [TodoItem] {
        let data = try await request(path: "/todos?limit=100", method: "GET")
        return try JSONDecoder().decode(TodoListResponse.self, from: data).rows
    }

    func create(title: String) async throws {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        let payload: [String: Any] = [
            "id": "todo-\(Int(Date().timeIntervalSince1970 * 1000))",
            "title": trimmed,
            "done": false,
            "owner_id": "alice",
        ]
        _ = try await request(path: "/todos", method: "POST", body: payload)
    }

    func setDone(id: String, done: Bool) async throws {
        _ = try await request(path: "/todos/\(id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id)", method: "PATCH", body: ["done": done])
    }

    func delete(id: String) async throws {
        _ = try await request(path: "/todos/\(id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id)", method: "DELETE")
    }

    private func request(path: String, method: String, body: [String: Any]? = nil) async throws -> Data {
        let root = baseUrl.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: "\(root)/data/\(slug)\(path)") else {
            throw URLError(.badURL)
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status >= 200 && status < 300 else {
            let text = String(data: data, encoding: .utf8) ?? "Yaver Serverless request failed"
            throw NSError(domain: "YaverTodoAPI", code: status, userInfo: [NSLocalizedDescriptionKey: text])
        }
        return data
    }
}
