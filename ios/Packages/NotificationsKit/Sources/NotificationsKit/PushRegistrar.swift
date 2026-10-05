import Foundation

/// APNs registration + push routing scaffold (NotificationsKit).
public struct PushRegistrar: Sendable {
    public init() {}

    /// Upload the APNs device token to the server (POST /v1/push/devices).
    public func register(token: Data, baseURL: URL) async throws {
        let _ = token // TODO: POST {device_token} to the configured endpoint.
        let _ = baseURL
    }

    /// Maps an inbound push payload to a deep-link route.
    public static func deepLink(payload: [AnyHashable: Any]) -> String? {
        guard let type = payload["type"] as? String else { return nil }
        switch type {
        case "vehicle": return "/vehicle/\(payload["vehicle_id"] ?? "")"
        case "charge_complete": return "/vehicle/\(payload["vehicle_id"] ?? "")/charging"
        default: return nil
        }
    }
}
