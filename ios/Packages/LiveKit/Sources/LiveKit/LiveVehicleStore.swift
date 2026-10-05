import Foundation
import CoreKit
import Combine

/// Live-status state machine: merges WebSocket deltas into a single
/// @Observable vehicle store for the UI (ADR-004: streaming-first).
@MainActor
@Observable
public final class LiveVehicleStore {
    public private(set) var states: [String: LiveState] = [:]
    public private(set) var lastUpdatedAt: Date?
    private let socket = VehicleLiveSocket()

    public init() {}

    public func connect(baseURL: URL, vehicleID: String, token: String?) async {
        let stream = await socket.connect(baseURL: baseURL, vehicleID: vehicleID, token: token)
        for await state in stream {
            states[state.vehicleId] = state
            lastUpdatedAt = Date()
        }
    }

    public func disconnect() async {
        await socket.disconnect()
    }

    public func state(for vehicleID: String) -> LiveState? { states[vehicleID] }
}
