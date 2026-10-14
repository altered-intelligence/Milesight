import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// Abstracts the network for testing.
public protocol HTTPTransport: Sendable {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

/// URLSession-backed transport.
public struct URLSessionTransport: HTTPTransport {
    public let session: URLSession
    public init(session: URLSession = .shared) { self.session = session }

    public func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw MilesightError.network("not an HTTP response") }
        return (data, http)
    }
}

/// The v1 API client.
///
/// - Attaches the access token, refreshes it on 401 and retries once.
/// - Serializes refreshes (the server revokes a session if a refresh token is
///   used twice, so concurrent 401s must share one refresh).
/// - Reports sign-out through `sessionChanges`.
public actor APIClient {
    public nonisolated let baseURL: URL
    private let transport: HTTPTransport
    private let tokens: TokenStore
    private var refreshTask: Task<Session, Error>?
    private var continuations: [UUID: AsyncStream<Session?>.Continuation] = [:]

    public init(baseURL: URL, tokens: TokenStore, transport: HTTPTransport = URLSessionTransport()) {
        self.baseURL = baseURL
        self.tokens = tokens
        self.transport = transport
    }

    // MARK: Session

    public var session: Session? { tokens.load() }
    public var accessToken: String? { tokens.load()?.accessToken }

    /// Emits the new session whenever it changes (nil = signed out).
    public func sessionChanges() -> AsyncStream<Session?> {
        let id = UUID()
        return AsyncStream { continuation in
            continuations[id] = continuation
            continuation.onTermination = { [weak self] _ in
                Task { await self?.removeContinuation(id) }
            }
        }
    }

    private func removeContinuation(_ id: UUID) { continuations[id] = nil }

    public func setSession(_ session: Session?) {
        tokens.save(session)
        for c in continuations.values { c.yield(session) }
    }

    /// A valid access token, refreshing first if it expires within a minute.
    public func validAccessToken() async throws -> String {
        guard let s = tokens.load() else { throw MilesightError.signedOut }
        if s.expiresAt.timeIntervalSinceNow > 60 { return s.accessToken }
        return try await refreshSession().accessToken
    }

    /// Exchanges the refresh token for a new session. Concurrent callers share
    /// one in-flight refresh (a refresh token may only ever be used once).
    @discardableResult
    public func refreshSession() async throws -> Session {
        if let task = refreshTask { return try await task.value }
        guard let current = tokens.load() else { throw MilesightError.signedOut }
        let task = Task { () throws -> Session in
            defer { self.refreshTask = nil }
            let (data, http) = try await self.perform(.refresh(refreshToken: current.refreshToken), token: nil)
            guard (200..<300).contains(http.statusCode) else {
                if http.statusCode == 401 || http.statusCode == 400 {
                    self.setSession(nil)
                    throw MilesightError.signedOut
                }
                throw Self.error(from: data, status: http.statusCode)
            }
            let s: Session
            do {
                s = try MilesightJSON.decoder.decode(Session.self, from: data)
            } catch {
                throw MilesightError.decoding(String(describing: error))
            }
            self.setSession(s)
            return s
        }
        refreshTask = task
        return try await task.value
    }

    // MARK: Requests

    /// Sends an endpoint and decodes the JSON response.
    public func send<T: Decodable>(_ endpoint: Endpoint, as: T.Type = T.self) async throws -> T {
        let data = try await sendData(endpoint)
        do {
            return try MilesightJSON.decoder.decode(T.self, from: data)
        } catch {
            throw MilesightError.decoding(String(describing: error))
        }
    }

    /// Sends an endpoint whose response body is irrelevant.
    public func sendVoid(_ endpoint: Endpoint) async throws {
        _ = try await sendData(endpoint)
    }

    /// Sends an endpoint and returns the raw body (e.g. CSV export).
    public func sendData(_ endpoint: Endpoint) async throws -> Data {
        var token: String?
        if endpoint.requiresAuth { token = try await validAccessToken() }
        var (data, http) = try await perform(endpoint, token: token)
        if http.statusCode == 401 && endpoint.requiresAuth {
            // If another request already refreshed while this one was in flight,
            // just retry with the new token instead of refreshing again.
            if let current = tokens.load()?.accessToken, current != token {
                token = current
            } else {
                token = try await refreshSession().accessToken
            }
            (data, http) = try await perform(endpoint, token: token)
            if http.statusCode == 401 {
                setSession(nil)
                throw MilesightError.signedOut
            }
        }
        guard (200..<300).contains(http.statusCode) else {
            if http.statusCode == 409, let refused = try? MilesightJSON.decoder.decode(CommandResult.self, from: data) {
                throw MilesightError.commandRefused(refused)
            }
            throw Self.error(from: data, status: http.statusCode)
        }
        return data
    }

    private func perform(_ endpoint: Endpoint, token: String?) async throws -> (Data, HTTPURLResponse) {
        guard let url = endpoint.url(base: baseURL) else { throw MilesightError.invalidURL }
        var req = URLRequest(url: url)
        req.httpMethod = endpoint.method
        req.timeoutInterval = endpoint.path.contains("/commands/") || endpoint.path.hasSuffix("/wake") ? 75 : 30
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body = endpoint.body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let key = endpoint.idempotencyKey { req.setValue(key, forHTTPHeaderField: "Idempotency-Key") }
        do {
            return try await transport.data(for: req)
        } catch let e as MilesightError {
            throw e
        } catch {
            throw MilesightError.network(error.localizedDescription)
        }
    }

    static func error(from data: Data, status: Int) -> MilesightError {
        if let env = try? MilesightJSON.decoder.decode(APIErrorEnvelope.self, from: data) {
            if env.error.code == "tesla_reauth_required" { return .teslaReauthRequired }
            return .server(status: status, code: env.error.code, message: env.error.message, requestId: env.error.requestId)
        }
        return .server(status: status, code: "http_\(status)", message: HTTPURLResponse.localizedString(forStatusCode: status), requestId: nil)
    }

    /// WebSocket URL for a vehicle's live stream.
    public nonisolated func liveSocketURL(vehicleID: String) -> URL? {
        guard var comps = URLComponents(url: Endpoint("GET", "/v1/vehicles/\(vehicleID)/ws").url(base: baseURL) ?? baseURL,
                                        resolvingAgainstBaseURL: false) else { return nil }
        comps.scheme = comps.scheme == "https" ? "wss" : "ws"
        return comps.url
    }
}
