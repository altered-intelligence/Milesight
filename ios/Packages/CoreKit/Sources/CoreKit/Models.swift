import Foundation

// Mirrors docs/api.md exactly. Property names are the camelCase form of the
// server's snake_case keys (MilesightJSON converts). Units are SI; format them
// for display with `UnitFormatter`.

// MARK: - Errors & config

public struct APIErrorBody: Codable, Sendable, Hashable {
    public var code: String
    public var message: String
    public var requestId: String?
}

public struct APIErrorEnvelope: Codable, Sendable {
    public var error: APIErrorBody
}

public struct ServerConfig: Codable, Sendable, Hashable {
    public var version: String
    public var transport: String
    public var devLogin: Bool
    public var teslaOauth: Bool
    public var ownerTokenLogin: Bool
    public var regions: [String]
}

// MARK: - Users & sessions

public struct Preferences: Codable, Sendable, Hashable {
    public var distanceUnit: String      // km | mi
    public var temperatureUnit: String   // c | f
    public var currency: String          // ISO 4217
    public var energyRatePerKwh: Double
    public var fastChargeRatePerKwh: Double
    public var timeZone: String          // IANA

    public init(distanceUnit: String = "mi", temperatureUnit: String = "f", currency: String = "USD",
                energyRatePerKwh: Double = 0.15, fastChargeRatePerKwh: Double = 0.45, timeZone: String = "UTC") {
        self.distanceUnit = distanceUnit
        self.temperatureUnit = temperatureUnit
        self.currency = currency
        self.energyRatePerKwh = energyRatePerKwh
        self.fastChargeRatePerKwh = fastChargeRatePerKwh
        self.timeZone = timeZone
    }

    public var usesMiles: Bool { distanceUnit == "mi" }
    public var usesFahrenheit: Bool { temperatureUnit == "f" }
}

public struct User: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var email: String
    public var plan: String
    public var transport: String?
    public var createdAt: Date
    public var preferences: Preferences
}

public struct Session: Codable, Sendable, Hashable {
    public var accessToken: String
    public var refreshToken: String
    public var tokenType: String
    public var expiresIn: Int
    public var expiresAt: Date
    public var user: User
}

/// Partial preferences update for PATCH /v1/me (nil fields are omitted).
public struct PreferencesPatch: Encodable, Sendable {
    public var distanceUnit: String?
    public var temperatureUnit: String?
    public var currency: String?
    public var energyRatePerKwh: Double?
    public var fastChargeRatePerKwh: Double?
    public var timeZone: String?

    public init(distanceUnit: String? = nil, temperatureUnit: String? = nil, currency: String? = nil,
                energyRatePerKwh: Double? = nil, fastChargeRatePerKwh: Double? = nil, timeZone: String? = nil) {
        self.distanceUnit = distanceUnit
        self.temperatureUnit = temperatureUnit
        self.currency = currency
        self.energyRatePerKwh = energyRatePerKwh
        self.fastChargeRatePerKwh = fastChargeRatePerKwh
        self.timeZone = timeZone
    }
}

// MARK: - Vehicles & live state

public enum VehicleStateValue {
    public static let online = "online"
    public static let asleep = "asleep"
    public static let offline = "offline"
}

public enum ChargeStateValue {
    public static let disconnected = "disconnected"
    public static let noPower = "no_power"
    public static let starting = "starting"
    public static let charging = "charging"
    public static let stopped = "stopped"
    public static let complete = "complete"
}

public struct Vehicle: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var vin: String
    public var displayName: String
    public var model: String?
    public var trim: String?
    public var color: String?
    public var region: String
    public var role: String
    public var state: String
    public var syncStatus: String
    public var lastSeen: Date?
    public var latest: LiveState?

    public var isOwner: Bool { role == "owner" }
    public var name: String { displayName.isEmpty ? (model ?? "Tesla") : displayName }

    public init(id: String, vin: String, displayName: String, model: String? = nil, trim: String? = nil, color: String? = nil,
                region: String = "na", role: String = "owner", state: String = VehicleStateValue.online,
                syncStatus: String = "polling", lastSeen: Date? = nil, latest: LiveState? = nil) {
        self.id = id; self.vin = vin; self.displayName = displayName; self.model = model; self.trim = trim
        self.color = color; self.region = region; self.role = role; self.state = state; self.syncStatus = syncStatus
        self.lastSeen = lastSeen; self.latest = latest
    }
}

public struct LiveState: Codable, Sendable, Hashable {
    public var vehicleId: String
    public var state: String
    public var battery: Battery
    public var charging: Charging?
    public var climate: Climate?
    public var drive: Drive?
    public var location: Location?
    public var closures: Closures?
    public var software: Software?
    public var odometerKm: Double
    public var updatedAt: Date

    public init(vehicleId: String, state: String, battery: Battery, charging: Charging? = nil, climate: Climate? = nil,
                drive: Drive? = nil, location: Location? = nil, closures: Closures? = nil, software: Software? = nil,
                odometerKm: Double = 0, updatedAt: Date) {
        self.vehicleId = vehicleId
        self.state = state
        self.battery = battery
        self.charging = charging
        self.climate = climate
        self.drive = drive
        self.location = location
        self.closures = closures
        self.software = software
        self.odometerKm = odometerKm
        self.updatedAt = updatedAt
    }

    public var isOnline: Bool { state == VehicleStateValue.online }
    public var isDriving: Bool {
        guard isOnline, let d = drive else { return false }
        return ["D", "R", "N"].contains(d.shiftState) || d.speedKph > 0
    }
    public var isCharging: Bool {
        isOnline && (battery.chargeState == ChargeStateValue.charging || battery.chargeState == ChargeStateValue.starting)
    }
}

public struct Battery: Codable, Sendable, Hashable {
    public var percent: Int
    public var usablePercent: Int
    public var rangeKm: Double
    public var idealRangeKm: Double?
    public var estRangeKm: Double?
    public var chargeLimitPct: Int
    public var chargeState: String
    public var pluggedIn: Bool
    public var chargePortOpen: Bool

    public init(percent: Int, usablePercent: Int? = nil, rangeKm: Double, idealRangeKm: Double? = nil, estRangeKm: Double? = nil,
                chargeLimitPct: Int = 80, chargeState: String = ChargeStateValue.disconnected, pluggedIn: Bool = false,
                chargePortOpen: Bool = false) {
        self.percent = percent
        self.usablePercent = usablePercent ?? percent
        self.rangeKm = rangeKm
        self.idealRangeKm = idealRangeKm
        self.estRangeKm = estRangeKm
        self.chargeLimitPct = chargeLimitPct
        self.chargeState = chargeState
        self.pluggedIn = pluggedIn
        self.chargePortOpen = chargePortOpen
    }
}

public struct Charging: Codable, Sendable, Hashable {
    public var powerKw: Double
    public var amps: Int
    public var ampsRequest: Int
    public var ampsMax: Int
    public var voltage: Int
    public var phases: Int?
    public var energyAddedKwh: Double
    public var rangeAddedKm: Double
    public var rateKph: Double
    public var minutesToLimit: Int
    public var fastCharger: Bool
    public var fastChargerType: String?

    public init(powerKw: Double = 0, amps: Int = 0, ampsRequest: Int = 0, ampsMax: Int = 0, voltage: Int = 0, phases: Int? = nil,
                energyAddedKwh: Double = 0, rangeAddedKm: Double = 0, rateKph: Double = 0, minutesToLimit: Int = 0,
                fastCharger: Bool = false, fastChargerType: String? = nil) {
        self.powerKw = powerKw; self.amps = amps; self.ampsRequest = ampsRequest; self.ampsMax = ampsMax
        self.voltage = voltage; self.phases = phases; self.energyAddedKwh = energyAddedKwh; self.rangeAddedKm = rangeAddedKm
        self.rateKph = rateKph; self.minutesToLimit = minutesToLimit; self.fastCharger = fastCharger
        self.fastChargerType = fastChargerType
    }
}

public struct Climate: Codable, Sendable, Hashable {
    public var on: Bool
    public var insideTempC: Double?
    public var outsideTempC: Double?
    public var driverSetC: Double
    public var passengerSetC: Double
    public var preconditioning: Bool
    public var defrostOn: Bool
    public var fanStatus: Int

    public init(on: Bool, insideTempC: Double? = nil, outsideTempC: Double? = nil, driverSetC: Double = 21,
                passengerSetC: Double = 21, preconditioning: Bool = false, defrostOn: Bool = false, fanStatus: Int = 0) {
        self.on = on; self.insideTempC = insideTempC; self.outsideTempC = outsideTempC; self.driverSetC = driverSetC
        self.passengerSetC = passengerSetC; self.preconditioning = preconditioning; self.defrostOn = defrostOn
        self.fanStatus = fanStatus
    }
}

public struct Drive: Codable, Sendable, Hashable {
    public var shiftState: String
    public var speedKph: Double
    public var powerKw: Double

    public init(shiftState: String = "P", speedKph: Double = 0, powerKw: Double = 0) {
        self.shiftState = shiftState; self.speedKph = speedKph; self.powerKw = powerKw
    }
}

public struct Location: Codable, Sendable, Hashable {
    public var lat: Double
    public var lon: Double
    public var headingDeg: Int
    public var place: String?

    public init(lat: Double, lon: Double, headingDeg: Int = 0, place: String? = nil) {
        self.lat = lat; self.lon = lon; self.headingDeg = headingDeg; self.place = place
    }
}

public struct Closures: Codable, Sendable, Hashable {
    public var locked: Bool
    public var doorsOpen: Bool
    public var windowsOpen: Bool
    public var frunkOpen: Bool
    public var trunkOpen: Bool
    public var sentryMode: Bool
    public var userPresent: Bool

    public init(locked: Bool = true, doorsOpen: Bool = false, windowsOpen: Bool = false, frunkOpen: Bool = false,
                trunkOpen: Bool = false, sentryMode: Bool = false, userPresent: Bool = false) {
        self.locked = locked; self.doorsOpen = doorsOpen; self.windowsOpen = windowsOpen; self.frunkOpen = frunkOpen
        self.trunkOpen = trunkOpen; self.sentryMode = sentryMode; self.userPresent = userPresent
    }
}

public struct Software: Codable, Sendable, Hashable {
    public var version: String
    public var updateAvailable: Bool
    public var updateVersion: String?
    public var updateStatus: String?

    public init(version: String, updateAvailable: Bool = false, updateVersion: String? = nil, updateStatus: String? = nil) {
        self.version = version; self.updateAvailable = updateAvailable; self.updateVersion = updateVersion
        self.updateStatus = updateStatus
    }
}

public struct VehicleList: Codable, Sendable {
    public var vehicles: [Vehicle]
}

public struct WakeResult: Codable, Sendable {
    public var state: String
    public var wokeUp: Bool
}

// MARK: - Commands

public struct CommandResult: Codable, Sendable, Hashable {
    public var requestId: String
    public var command: String
    public var status: String       // ok | failed
    public var reason: String?
    public var wokeUp: Bool
    public var at: Date

    public var succeeded: Bool { status == "ok" }
}

// MARK: - History & analytics

public struct GeoPoint: Codable, Sendable, Hashable {
    public var lat: Double
    public var lon: Double
}

public struct Page<T: Codable & Sendable>: Codable, Sendable {
    public var items: [T]
    public var nextCursor: String?
}

public struct TelemetryPoint: Codable, Sendable, Hashable {
    public var time: Date
    public var state: String?
    public var batteryPct: Double?
    public var ratedRangeKm: Double?
    public var lat: Double?
    public var lon: Double?
    public var speedKph: Double?
    public var powerKw: Double?
    public var odometerKm: Double?
    public var insideTempC: Double?
    public var outsideTempC: Double?
    public var chargerPowerKw: Double?
    public var chargeEnergyAddedKwh: Double?
}

public struct TelemetryResponse: Codable, Sendable {
    public var from: Date
    public var to: Date
    public var resolution: String
    public var points: [TelemetryPoint]
}

public struct Trip: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var vehicleId: String
    public var startedAt: Date
    public var endedAt: Date?
    public var startOdometerKm: Double
    public var endOdometerKm: Double
    public var distanceKm: Double
    public var durationS: Int
    public var avgSpeedKph: Double
    public var maxSpeedKph: Double
    public var startBatteryPct: Int
    public var endBatteryPct: Int
    public var startRangeKm: Double
    public var endRangeKm: Double
    public var energyUsedKwh: Double
    public var efficiencyWhKm: Double?
    public var startLocation: GeoPoint?
    public var endLocation: GeoPoint?
    public var startPlace: String?
    public var endPlace: String?
    public var outsideTempC: Double?

    public var inProgress: Bool { endedAt == nil }
}

public struct RoutePoint: Codable, Sendable, Hashable {
    public var time: Date
    public var lat: Double
    public var lon: Double
    public var speedKph: Double
    public var powerKw: Double
    public var batteryPct: Double
}

/// GET /trips/{id}: the trip's fields plus its route (flattened on the wire).
public struct TripDetail: Codable, Sendable {
    public var trip: Trip
    public var route: [RoutePoint]

    private enum CodingKeys: String, CodingKey { case route }

    public init(from decoder: Decoder) throws {
        trip = try Trip(from: decoder)
        route = try decoder.container(keyedBy: CodingKeys.self).decodeIfPresent([RoutePoint].self, forKey: .route) ?? []
    }

    public func encode(to encoder: Encoder) throws {
        try trip.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(route, forKey: .route)
    }
}

public struct ChargeSession: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var vehicleId: String
    public var startedAt: Date
    public var endedAt: Date?
    public var durationS: Int
    public var startBatteryPct: Int
    public var endBatteryPct: Int
    public var startRangeKm: Double
    public var endRangeKm: Double
    public var energyAddedKwh: Double
    public var maxPowerKw: Double
    public var avgPowerKw: Double
    public var fastCharger: Bool
    public var chargerType: String?
    public var location: GeoPoint?
    public var place: String?
    public var cost: Double?
    public var currency: String?
    public var endReason: String?

    public var inProgress: Bool { endedAt == nil }
}

public struct ChargePoint: Codable, Sendable, Hashable {
    public var time: Date
    public var batteryPct: Double
    public var powerKw: Double
    public var voltage: Double
    public var amps: Double
    public var energyAddedKwh: Double
}

/// GET /charges/{id}: the session's fields plus its charging curve.
public struct ChargeDetail: Codable, Sendable {
    public var session: ChargeSession
    public var curve: [ChargePoint]

    private enum CodingKeys: String, CodingKey { case curve }

    public init(from decoder: Decoder) throws {
        session = try ChargeSession(from: decoder)
        curve = try decoder.container(keyedBy: CodingKeys.self).decodeIfPresent([ChargePoint].self, forKey: .curve) ?? []
    }

    public func encode(to encoder: Encoder) throws {
        try session.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(curve, forKey: .curve)
    }
}

public struct StatsBucket: Codable, Sendable, Hashable {
    public var start: Date
    public var end: Date
    public var distanceKm: Double
    public var driveTimeS: Int
    public var trips: Int
    public var energyUsedKwh: Double
    public var efficiencyWhKm: Double?
    public var energyAddedKwh: Double
    public var charges: Int
    public var chargingCost: Double
    public var drainKm: Double
    public var drainKwh: Double
    public var idleHours: Double

    public init(start: Date, end: Date, distanceKm: Double = 0, driveTimeS: Int = 0, trips: Int = 0, energyUsedKwh: Double = 0,
                efficiencyWhKm: Double? = nil, energyAddedKwh: Double = 0, charges: Int = 0, chargingCost: Double = 0,
                drainKm: Double = 0, drainKwh: Double = 0, idleHours: Double = 0) {
        self.start = start; self.end = end; self.distanceKm = distanceKm; self.driveTimeS = driveTimeS; self.trips = trips
        self.energyUsedKwh = energyUsedKwh; self.efficiencyWhKm = efficiencyWhKm; self.energyAddedKwh = energyAddedKwh
        self.charges = charges; self.chargingCost = chargingCost; self.drainKm = drainKm; self.drainKwh = drainKwh
        self.idleHours = idleHours
    }
}

public enum StatsPeriod: String, CaseIterable, Sendable, Identifiable {
    case daily, weekly, monthly
    public var id: String { rawValue }
}

public struct StatsResponse: Codable, Sendable {
    public var period: String
    public var timeZone: String
    public var currency: String
    public var from: Date
    public var to: Date
    public var buckets: [StatsBucket]
    public var totals: StatsBucket

    public init(period: String, timeZone: String, currency: String, from: Date, to: Date, buckets: [StatsBucket], totals: StatsBucket) {
        self.period = period; self.timeZone = timeZone; self.currency = currency; self.from = from; self.to = to
        self.buckets = buckets; self.totals = totals
    }
}

public struct BatteryHealth: Codable, Sendable {
    public var nominalCapacityKwh: Double
    public var estimatedCapacityKwh: Double?
    public var nominalFullRangeKm: Double
    public var fullRangeKm: Double?
    public var degradationPct: Double?
    public var samples: Int
    public var history: [BatteryHealthPoint]
}

public struct BatteryHealthPoint: Codable, Sendable, Hashable {
    public var month: Date
    public var capacityKwh: Double?
    public var fullRangeKm: Double?
    public var samples: Int
}

// MARK: - Places, rules, events, devices

public struct Geofence: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var name: String
    public var lat: Double
    public var lon: Double
    public var radiusM: Double
    public var energyRatePerKwh: Double?

    public init(id: String = "", name: String, lat: Double, lon: Double, radiusM: Double = 150, energyRatePerKwh: Double? = nil) {
        self.id = id
        self.name = name
        self.lat = lat
        self.lon = lon
        self.radiusM = radiusM
        self.energyRatePerKwh = energyRatePerKwh
    }
}

public struct GeofenceList: Codable, Sendable { public var geofences: [Geofence] }

/// Body for creating/updating a place. `clearEnergyRate` removes a place-specific rate.
public struct GeofenceInput: Encodable, Sendable {
    public var name: String?
    public var lat: Double?
    public var lon: Double?
    public var radiusM: Double?
    public var energyRatePerKwh: Double?
    public var clearEnergyRate: Bool?

    public init(name: String? = nil, lat: Double? = nil, lon: Double? = nil, radiusM: Double? = nil,
                energyRatePerKwh: Double? = nil, clearEnergyRate: Bool? = nil) {
        self.name = name
        self.lat = lat
        self.lon = lon
        self.radiusM = radiusM
        self.energyRatePerKwh = energyRatePerKwh
        self.clearEnergyRate = clearEnergyRate
    }
}

public struct Rule: Codable, Sendable, Hashable, Identifiable {
    public var type: String
    public var enabled: Bool
    /// camelCase keys in Swift (e.g. "thresholdPct", "minutes"); see MilesightJSON.
    public var params: [String: Double]?

    public var id: String { type }

    public init(type: String, enabled: Bool, params: [String: Double]? = nil) {
        self.type = type
        self.enabled = enabled
        self.params = params
    }
}

public struct RuleList: Codable, Sendable { public var rules: [Rule] }

public struct Event: Codable, Sendable, Hashable, Identifiable {
    public var id: Int64
    public var vehicleId: String
    public var type: String
    public var title: String
    public var body: String
    public var occurredAt: Date
    /// camelCase keys in Swift (e.g. "deepLink", "batteryPct").
    public var payload: [String: JSONValue]?
    public var deliveredAt: Date?

    public var deepLink: URL? { payload?["deepLink"]?.string.flatMap(URL.init(string:)) }
}

public struct PushDevice: Codable, Sendable, Hashable {
    public var id: String
    public var token: String
    public var platform: String
    public var environment: String
    public var createdAt: Date
}

public struct BillingPlan: Codable, Sendable {
    public var plan: String
    public var status: String
    public var store: String
}

public struct AuthorizeResponse: Codable, Sendable {
    public var authorizeUrl: String
    public var state: String
    public var redirectUri: String
    public var appRedirect: String
}

public struct AccountDeletion: Codable, Sendable {
    public var status: String
    public var revokeTeslaAccessUrl: String?
}
