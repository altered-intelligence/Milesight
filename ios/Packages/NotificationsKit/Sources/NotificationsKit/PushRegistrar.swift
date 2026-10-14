import Foundation
import CoreKit

/// Uploads the APNs device token so the server can push to this device.
public struct PushRegistrar: Sendable {
    public let api: APIClient
    /// "sandbox" for development builds, "production" for TestFlight/App Store.
    public let environment: String

    public init(api: APIClient, environment: String) {
        self.api = api
        self.environment = environment
    }

    /// Hex encoding APNs expects.
    public static func hex(_ token: Data) -> String { token.map { String(format: "%02x", $0) }.joined() }

    public func register(deviceToken: Data) async throws {
        let _: PushDevice = try await api.send(.registerDevice(token: Self.hex(deviceToken), environment: environment))
    }

    public func unregister(deviceToken: Data) async throws {
        try await api.sendVoid(.deleteDevice(token: Self.hex(deviceToken)))
    }
}
