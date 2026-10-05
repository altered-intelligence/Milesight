import Foundation

/// Streaming-first realtime client: subscribes to /v1/vehicles/{id}/ws and
/// fans live deltas into an AsyncStream (ADR-004).
public actor VehicleLiveSocket {
    private var task: Task<Void, Never>?
    private var continuation: AsyncStream<LiveState>.Continuation?

    public init() {}

    public func connect(baseURL: URL, vehicleID: String, token: String?) -> AsyncStream<LiveState> {
        disconnect()
        let stream = AsyncStream<LiveState> { continuation in
            self.continuation = continuation
        }

        var comps = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)
        comps?.scheme = baseURL.scheme == "https" ? "wss" : "ws"
        comps?.path = "/v1/vehicles/\(vehicleID)/ws"

        guard let url = comps?.url else {
            continuation?.finish()
            return stream
        }

        var request = URLRequest(url: url)
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let wsTask = URLSession.shared.webSocketTask(with: request)

        task = Task { [weak self] in
            await self?.receiveLoop(wsTask)
        }
        wsTask.resume()
        return stream
    }

    private func receiveLoop(_ ws: URLSessionWebSocketTask) async {
        do {
            while true {
                let msg = try await ws.receive()
                guard case .string(let text) = msg,
                      let data = text.data(using: .utf8),
                      let state = try? JSONDecoder().decode(LiveState.self, from: data)
                else { continue }
                continuation?.yield(state)
            }
        } catch {
            continuation?.finish()
        }
    }

    public func disconnect() {
        task?.cancel()
        task = nil
        continuation?.finish()
        continuation = nil
    }
}
