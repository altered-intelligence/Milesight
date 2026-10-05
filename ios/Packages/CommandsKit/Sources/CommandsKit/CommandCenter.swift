import Foundation
import CoreKit
import Combine

/// Command surface with optimistic updates + rollback. Sends the intent
/// immediately, then reconciles with authoritative server state.
@MainActor
@Observable
public final class CommandCenter {
    public enum CommandKind: String, Sendable {
        case climate
        case lock
        case unlock
        case frunk
        case trunk
        case chargePort
        case chargePortClose = "charge_port_close"
        case honk
        case flashLights = "flash_lights"
    }

    public private(set) var pending: [String: CommandKind] = [:] // vehicleID -> command
    private let client: APIClient

    public init(client: APIClient) { self.client = client }

    /// Fire-and-forget command with optimistic tracking; state reconciliation
    /// is driven by the live stream in LiveKit.
    public func send(vehicleID: String, kind: CommandKind) async throws {
        pending[vehicleID] = kind
        defer { pending[vehicleID] = nil }
        let _: CommandResponse = try await client.post(
            APIRouter.command(vehicleID: vehicleID, name: kind.rawValue), body: EmptyBody())
    }
}

private struct EmptyBody: Encodable {}
