import Foundation

/// Mirrors docs/api.md — contract-first domain types (Codable for JSON transport).
public struct Vehicle: Codable, Identifiable, Sendable {
    public let id: String
    public let displayName: String?
    public let vin: String?
    public let syncStatus: String?
    public let lastSeen: Date?
    public let state: String?

    public init(id: String, displayName: String?, vin: String?, syncStatus: String?,
                lastSeen: Date?, state: String?) {
        self.id = id
        self.displayName = displayName
        self.vin = vin
        self.syncStatus = syncStatus
        self.lastSeen = lastSeen
        self.state = state
    }
}

public struct LiveState: Codable, Sendable, Equatable {
    public let vehicleId: String
    public let battery: Battery
    public let location: Location?
    public let climate: Climate?
    public let locks: Locks?
    public let software: String?
    public let odometerM: Double?
    public let updatedAt: Date

    public init(vehicleId: String, battery: Battery, location: Location?, climate: Climate?,
                locks: Locks?, software: String?, odometerM: Double?, updatedAt: Date) {
        self.vehicleId = vehicleId
        self.battery = battery
        self.location = location
        self.climate = climate
        self.locks = locks
        self.software = software
        self.odometerM = odometerM
        self.updatedAt = updatedAt
    }
}

public struct Battery: Codable, Sendable, Equatable {
    public let percent: Int
    public let rangeKm: Double
    public let chargeState: String
    public let chargePort: String?
    public let chargingAmps: Int?
    public let chargeLimitPct: Int

    public init(percent: Int, rangeKm: Double, chargeState: String, chargePort: String?,
                chargingAmps: Int?, chargeLimitPct: Int) {
        self.percent = percent
        self.rangeKm = rangeKm
        self.chargeState = chargeState
        self.chargePort = chargePort
        self.chargingAmps = chargingAmps
        self.chargeLimitPct = chargeLimitPct
    }
}

public struct Location: Codable, Sendable, Equatable {
    public let lat: Double
    public let lon: Double
    public let heading: Int?
    public init(lat: Double, lon: Double, heading: Int?) { self.lat = lat; self.lon = lon; self.heading = heading }
}

public struct Climate: Codable, Sendable, Equatable {
    public let enabled: Bool
    public let tempC: Double?
    public init(enabled: Bool, tempC: Double?) { self.enabled = enabled; self.tempC = tempC }
}

public struct Locks: Codable, Sendable, Equatable {
    public let locked: Bool
    public let doors: Bool
    public let windows: Bool
    public let frunk: Bool
    public let trunk: Bool
    public init(locked: Bool, doors: Bool, windows: Bool, frunk: Bool, trunk: Bool) {
        self.locked = locked; self.doors = doors; self.windows = windows
        self.frunk = frunk; self.trunk = trunk
    }
}

public struct Geofence: Codable, Sendable, Identifiable, Equatable {
    public let id: String
    public let name: String
    public let lat: Double
    public let lon: Double
    public let radiusM: Double
    public init(id: String, name: String, lat: Double, lon: Double, radiusM: Double) {
        self.id = id; self.name = name; self.lat = lat; self.lon = lon; self.radiusM = radiusM
    }
}

public enum MilesightError: Error, Sendable {
    case invalidURL
    case transport
    case server(code: String, message: String)
    case decoding
}

/// Response envelope for vehicle commands (mirrors api.md).
public struct CommandResponse: Codable, Sendable, Equatable {
    public let requestId: String
    public let status: String
    public init(requestId: String, status: String) {
        self.requestId = requestId
        self.status = status
    }
}
