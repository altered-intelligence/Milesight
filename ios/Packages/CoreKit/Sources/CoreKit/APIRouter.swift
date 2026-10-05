import Foundation

/// Typed endpoints mirroring docs/api.md. Server is authoritative for time.
public enum APIRouter {
    case listVehicles
    case liveState(vehicleID: String)
    case wake(vehicleID: String)
    case command(vehicleID: String, name: String)
    case telemetry(vehicleID: String)

    var method: String {
        switch self {
        case .listVehicles, .liveState: return "GET"
        case .wake, .command: return "POST"
        case .telemetry: return "GET"
        }
    }

    var path: String {
        switch self {
        case .listVehicles: return "/v1/vehicles"
        case .liveState(let id): return "/v1/vehicles/\(id)/live"
        case .wake(let id): return "/v1/vehicles/\(id)/wake"
        case .command(let id, let name): return "/v1/vehicles/\(id)/commands/\(name)"
        case .telemetry(let id): return "/v1/vehicles/\(id)/telemetry"
        }
    }

    func url(base: URL) -> URL? {
        base.appendingPathComponent(path.trimmingCharacters(in: CharacterSet(charactersIn: "/")))
    }
}
