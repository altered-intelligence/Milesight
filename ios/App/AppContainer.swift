import Foundation
import CoreKit
import LiveKit
import CommandsKit

/// Shared dependency graph for the app target.
@MainActor
final class AppContainer: ObservableObject {
    static let shared = AppContainer()

    let api: APIClient
    let vehicles: LiveVehicleStore
    let commands: CommandCenter

    init() {
        // TODO: point at the real API + inject JWT via the auth flow.
        let base = URL(string: "https://api.yourdomain.com/v1")!
        self.api = APIClient(baseURL: base)
        self.vehicles = LiveVehicleStore()
        self.commands = CommandCenter(client: api)
    }
}
