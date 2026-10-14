import Foundation
import Observation
import CoreKit

/// Connection status of a vehicle's live stream.
public enum ConnectionState: Equatable, Sendable {
    case disconnected
    case connecting
    case live
    case retrying(attempt: Int)
}

/// The app's source of truth for vehicles and their live state (ADR-004:
/// push-driven). REST loads the initial picture; a WebSocket per watched
/// vehicle streams deltas; optimistic overlays keep the UI responsive while
/// commands are confirmed.
@MainActor
@Observable
public final class VehicleStore {
    public private(set) var vehicles: [Vehicle] = []
    public private(set) var live: [String: LiveState] = [:]
    public private(set) var activeTrip: [String: Trip] = [:]
    public private(set) var activeCharge: [String: ChargeSession] = [:]
    public private(set) var connection: [String: ConnectionState] = [:]
    public private(set) var recentEvents: [Event] = []
    public private(set) var isLoading = false
    public private(set) var lastRefresh: Date?
    public var lastError: MilesightError?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let makeSocket: @Sendable (URL, String) -> LiveSocket
    @ObservationIgnored private var streams: [String: Task<Void, Never>] = [:]
    @ObservationIgnored private var overlays: [String: [Overlay]] = [:]

    private struct Overlay {
        let id: UUID
        let until: Date
        let transform: @Sendable (inout LiveState) -> Void
    }

    public init(api: APIClient,
                makeSocket: @escaping @Sendable (URL, String) -> LiveSocket = { LiveSocket(url: $0, accessToken: $1) }) {
        self.api = api
        self.makeSocket = makeSocket
    }

    // MARK: Reading

    public func vehicle(_ id: String) -> Vehicle? { vehicles.first { $0.id == id } }

    /// Latest state for display: streamed state, else the snapshot from the list.
    public func state(for id: String) -> LiveState? { live[id] ?? vehicle(id)?.latest }

    // MARK: Loading

    public func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let list: VehicleList = try await api.send(.vehicles)
            vehicles = list.vehicles
            for v in list.vehicles {
                if let latest = v.latest { setLive(latest, for: v.id) }
            }
            lastRefresh = Date()
            lastError = nil
        } catch let e as MilesightError {
            lastError = e
        } catch {
            lastError = .network(error.localizedDescription)
        }
    }

    /// Re-lists vehicles from Tesla (e.g. after adding a car to the account).
    public func syncWithTesla() async {
        do {
            let list: VehicleList = try await api.send(.syncVehicles)
            vehicles = list.vehicles
        } catch let e as MilesightError {
            lastError = e
        } catch {
            lastError = .network(error.localizedDescription)
        }
    }

    public func refreshLive(_ id: String) async {
        do {
            let s: LiveState = try await api.send(.live(id))
            setLive(s, for: id)
        } catch let e as MilesightError {
            lastError = e
        } catch {
            lastError = .network(error.localizedDescription)
        }
    }

    public func rename(_ id: String, to name: String) async throws {
        let v: Vehicle = try await api.send(.renameVehicle(id, name: name))
        if let i = vehicles.firstIndex(where: { $0.id == id }) { vehicles[i] = v }
    }

    public func wake(_ id: String) async throws {
        let _: WakeResult = try await api.send(.wake(id))
        await refreshLive(id)
    }

    public func clear() {
        stopAll()
        vehicles = []
        live = [:]
        activeTrip = [:]
        activeCharge = [:]
        recentEvents = []
        overlays = [:]
    }

    // MARK: Streaming

    /// Starts (idempotently) streaming a vehicle; reconnects with backoff until stopped.
    public func startStreaming(_ id: String) {
        guard streams[id] == nil else { return }
        let api = self.api
        let makeSocket = self.makeSocket
        streams[id] = Task { [weak self] in
            var attempt = 0
            while !Task.isCancelled {
                self?.connection[id] = attempt == 0 ? .connecting : .retrying(attempt: attempt)
                do {
                    let token = try await api.validAccessToken()
                    guard let url = api.liveSocketURL(vehicleID: id) else { break }
                    let socket = makeSocket(url, token)
                    for try await message in socket.messages() {
                        guard let self else { return }
                        attempt = 0
                        self.connection[id] = .live
                        self.apply(message)
                    }
                } catch MilesightError.signedOut {
                    break
                } catch {
                    // fall through to backoff
                }
                if Task.isCancelled { break }
                attempt += 1
                let delay = min(30.0, pow(2.0, Double(min(attempt, 5))))
                try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            }
            self?.connection[id] = .disconnected
        }
    }

    public func stopStreaming(_ id: String) {
        streams[id]?.cancel()
        streams[id] = nil
        connection[id] = .disconnected
    }

    public func stopAll() {
        for id in streams.keys { stopStreaming(id) }
    }

    /// Applies one stream message (also used by tests and previews).
    public func apply(_ message: LiveMessage) {
        switch message {
        case .state(let s):
            setLive(s, for: s.vehicleId)
        case .trip(let t):
            activeTrip[t.vehicleId] = t.inProgress ? t : nil
        case .charge(let c):
            activeCharge[c.vehicleId] = c.inProgress ? c : nil
        case .event(let e):
            recentEvents.insert(e, at: 0)
            if recentEvents.count > 50 { recentEvents.removeLast(recentEvents.count - 50) }
        case .command, .unknown:
            break
        }
    }

    // MARK: Optimistic overlays

    /// Shows `transform` applied on top of server state for `duration`, so a
    /// command's effect appears instantly and stale reads can't flicker it back.
    /// Returns a token for `removeOverlay` (on failure).
    @discardableResult
    public func addOverlay(_ id: String, for duration: TimeInterval = 15,
                           _ transform: @escaping @Sendable (inout LiveState) -> Void) -> UUID {
        let token = UUID()
        overlays[id, default: []].append(Overlay(id: token, until: Date().addingTimeInterval(duration), transform: transform))
        if let s = live[id] ?? vehicle(id)?.latest { live[id] = overlaid(s, for: id) }
        return token
    }

    public func removeOverlay(_ id: String, token: UUID, restoring previous: LiveState?) {
        overlays[id]?.removeAll { $0.id == token }
        if let previous { live[id] = overlaid(previous, for: id) }
    }

    private func overlaid(_ s: LiveState, for id: String) -> LiveState {
        let now = Date()
        overlays[id]?.removeAll { $0.until < now }
        var out = s
        for o in overlays[id] ?? [] { o.transform(&out) }
        return out
    }

    private func setLive(_ s: LiveState, for id: String) {
        // Never go backwards in time (REST and WS can race).
        if let cur = live[id], cur.updatedAt > s.updatedAt, cur.state == s.state { return }
        live[id] = overlaid(s, for: id)
        if let i = vehicles.firstIndex(where: { $0.id == id }) {
            vehicles[i].state = s.state
            if s.state == VehicleStateValue.online { vehicles[i].lastSeen = s.updatedAt }
        }
    }
}
