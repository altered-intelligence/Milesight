import Foundation
import Observation
import CoreKit
import LiveKit

/// Sends commands with optimistic UI: the expected change shows immediately,
/// is held against stale reads while the car confirms, and rolls back on failure.
@MainActor
@Observable
public final class CommandCenter {
    public private(set) var inFlight: [String: Set<String>] = [:] // vehicleID -> command names
    public private(set) var lastResult: CommandResult?
    public var lastError: MilesightError?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let store: VehicleStore

    public init(api: APIClient, store: VehicleStore) {
        self.api = api
        self.store = store
    }

    public func isSending(_ command: VehicleCommand, on vehicleID: String) -> Bool {
        inFlight[vehicleID]?.contains(command.name) ?? false
    }

    public func isBusy(_ vehicleID: String) -> Bool { !(inFlight[vehicleID]?.isEmpty ?? true) }

    /// Sends a command; returns true on success. Errors land in `lastError`.
    @discardableResult
    public func send(_ command: VehicleCommand, to vehicleID: String) async -> Bool {
        inFlight[vehicleID, default: []].insert(command.name)
        defer { inFlight[vehicleID]?.remove(command.name) }

        let previous = store.state(for: vehicleID)
        let overlay = store.addOverlay(vehicleID, for: 20) { command.applyOptimistically(&$0) }
        do {
            let result: CommandResult = try await api.send(.command(vehicleID, name: command.name, body: command.body))
            lastResult = result
            lastError = nil
            return true
        } catch let e as MilesightError {
            store.removeOverlay(vehicleID, token: overlay, restoring: previous)
            lastError = e
            if case .commandRefused(let r) = e { lastResult = r }
            return false
        } catch {
            store.removeOverlay(vehicleID, token: overlay, restoring: previous)
            lastError = .network(error.localizedDescription)
            return false
        }
    }
}
