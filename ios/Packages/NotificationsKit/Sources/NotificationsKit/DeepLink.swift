import Foundation

/// In-app destinations reachable from notifications and URLs
/// (`milesight://vehicle/{id}[/charging|/charges/{cid}|/trips/{tid}]`).
public enum DeepLink: Equatable, Sendable {
    case vehicle(id: String)
    case charging(vehicleID: String)
    case charge(vehicleID: String, chargeID: String)
    case trip(vehicleID: String, tripID: String)
    /// OAuth return: `milesight://auth/callback?code&state` (or `error`).
    case authCallback(code: String?, state: String?, error: String?)

    public init?(url: URL) {
        guard url.scheme?.lowercased() == "milesight" else { return nil }
        // For custom schemes the first component lands in `host`.
        let parts = ([url.host].compactMap { $0 } + url.pathComponents.filter { $0 != "/" })
        let q = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func item(_ n: String) -> String? { q.first { $0.name == n }?.value }
        switch parts.first {
        case "auth" where parts.count >= 2 && parts[1] == "callback":
            self = .authCallback(code: item("code"), state: item("state"), error: item("error"))
        case "vehicle" where parts.count >= 2:
            let id = parts[1]
            switch parts.count {
            case 2: self = .vehicle(id: id)
            case 3 where parts[2] == "charging": self = .charging(vehicleID: id)
            case 4 where parts[2] == "charges": self = .charge(vehicleID: id, chargeID: parts[3])
            case 4 where parts[2] == "trips": self = .trip(vehicleID: id, tripID: parts[3])
            default: self = .vehicle(id: id)
            }
        default:
            return nil
        }
    }

    /// Parses the `deep_link` field of a push payload (APNs userInfo).
    public init?(pushPayload userInfo: [AnyHashable: Any]) {
        if let s = userInfo["deep_link"] as? String, let url = URL(string: s), let link = DeepLink(url: url) {
            self = link
        } else if let id = userInfo["vehicle_id"] as? String {
            self = .vehicle(id: id)
        } else {
            return nil
        }
    }

    public var vehicleID: String? {
        switch self {
        case .vehicle(let id), .charging(let id), .charge(let id, _), .trip(let id, _): return id
        case .authCallback: return nil
        }
    }
}
