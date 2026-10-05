import Foundation

/// Async/await JSON client for the v1 API. Injected for testability and
/// so we can swap the base URL / auth token per environment.
public struct APIClient: Sendable {
    public let baseURL: URL
    public var tokenProvider: @Sendable () -> String?

    public init(baseURL: URL, tokenProvider: @escaping @Sendable () -> String? = { nil }) {
        self.baseURL = baseURL
        self.tokenProvider = tokenProvider
    }

    public func get<T: Decodable>(_ route: APIRouter, as: T.Type = T.self) async throws -> T {
        guard let url = route.url(base: baseURL) else { throw MilesightError.invalidURL }
        var request = URLRequest(url: url)
        request.httpMethod = route.method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token = tokenProvider() {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return try await execute(request)
    }

    public func post<T: Decodable, B: Encodable>(_ route: APIRouter, body: B, as: T.Type = T.self) async throws -> T {
        guard let url = route.url(base: baseURL) else { throw MilesightError.invalidURL }
        var request = URLRequest(url: url)
        request.httpMethod = route.method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(body)
        if let token = tokenProvider() {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return try await execute(request)
    }

    private func execute<T: Decodable>(_ request: URLRequest) async throws -> T {
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw MilesightError.transport }
        guard (200..<300).contains(http.statusCode) else {
            if let err = try? JSONDecoder().decode(APIErrorEnvelope.self, from: data) {
                throw MilesightError.server(code: err.error.code, message: err.error.message)
            }
            throw MilesightError.server(code: "http_\(http.statusCode)", message: "Request failed")
        }
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw MilesightError.decoding
        }
    }
}

private struct APIErrorEnvelope: Decodable {
    struct Error: Decodable { let code: String; let message: String }
    let error: Error
}

/// Convenience list wrapper for `{ "vehicles": [...] }`.
public struct VehicleList: Decodable, Sendable { public let vehicles: [Vehicle] }
