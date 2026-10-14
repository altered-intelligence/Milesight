import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// One message from `/v1/vehicles/{id}/ws` (docs/api.md, "WebSocket").
public enum LiveMessage: Sendable {
    case state(LiveState)
    case trip(Trip)
    case charge(ChargeSession)
    case event(Event)
    case command(CommandResult)
    case unknown(String)

    private struct Head: Decodable { let type: String }
    private struct Body<T: Decodable>: Decodable { let data: T }

    /// Decodes an envelope `{"type", "vehicle_id", "at", "data"}`; unknown types are tolerated.
    public static func decode(_ data: Data) throws -> LiveMessage {
        let d = MilesightJSON.decoder
        let head = try d.decode(Head.self, from: data)
        switch head.type {
        case "state": return .state(try d.decode(Body<LiveState>.self, from: data).data)
        case "trip": return .trip(try d.decode(Body<Trip>.self, from: data).data)
        case "charge": return .charge(try d.decode(Body<ChargeSession>.self, from: data).data)
        case "event": return .event(try d.decode(Body<Event>.self, from: data).data)
        case "command": return .command(try d.decode(Body<CommandResult>.self, from: data).data)
        default: return .unknown(head.type)
        }
    }
}

/// A single WebSocket connection. Reconnection policy lives in the caller
/// (LiveKit), which knows whether anyone still cares about the vehicle.
public final class LiveSocket: @unchecked Sendable {
    private let task: URLSessionWebSocketTask

    public init(url: URL, accessToken: String, session: URLSession = .shared) {
        var req = URLRequest(url: url)
        req.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        task = session.webSocketTask(with: req)
    }

    /// Opens the connection and streams decoded messages until it closes.
    public func messages() -> AsyncThrowingStream<LiveMessage, Error> {
        task.resume()
        let task = self.task
        return AsyncThrowingStream { continuation in
            let reader = Task {
                do {
                    while !Task.isCancelled {
                        let msg = try await task.receive()
                        let data: Data
                        switch msg {
                        case .string(let s): data = Data(s.utf8)
                        case .data(let d): data = d
                        @unknown default: continue
                        }
                        if let decoded = try? LiveMessage.decode(data) {
                            continuation.yield(decoded)
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in
                reader.cancel()
                task.cancel(with: .goingAway, reason: nil)
            }
        }
    }

    /// Asks the server for an immediate read of the car.
    public func requestRefresh() {
        task.send(.string(#"{"type":"refresh"}"#)) { _ in }
    }

    public func close() {
        task.cancel(with: .goingAway, reason: nil)
    }
}
