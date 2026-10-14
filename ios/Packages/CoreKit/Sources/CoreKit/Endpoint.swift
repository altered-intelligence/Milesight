import Foundation

/// One HTTP request against the v1 API (docs/api.md).
public struct Endpoint: Sendable {
    public var method: String
    public var path: String
    public var query: [URLQueryItem]
    public var body: Data?
    public var requiresAuth: Bool
    public var idempotencyKey: String?

    public init(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil,
                requiresAuth: Bool = true, idempotencyKey: String? = nil) {
        self.method = method
        self.path = path
        self.query = query
        self.body = body
        self.requiresAuth = requiresAuth
        self.idempotencyKey = idempotencyKey
    }

    public func url(base: URL) -> URL? {
        var comps = URLComponents(url: base, resolvingAgainstBaseURL: false)
        let basePath = comps?.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")) ?? ""
        comps?.path = "/" + [basePath, path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))]
            .filter { !$0.isEmpty }.joined(separator: "/")
        if !query.isEmpty { comps?.queryItems = query }
        return comps?.url
    }

    static func json<B: Encodable>(_ value: B) -> Data? { try? MilesightJSON.encoder.encode(value) }
}

private struct EmptyBody: Encodable {}

private func pathEscape(_ s: String) -> String {
    s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s
}

private func timeItems(from: Date?, to: Date?) -> [URLQueryItem] {
    var q: [URLQueryItem] = []
    if let from { q.append(URLQueryItem(name: "from", value: RFC3339.format(from))) }
    if let to { q.append(URLQueryItem(name: "to", value: RFC3339.format(to))) }
    return q
}

/// Typed endpoints. Paths are relative to the server root (they include /v1).
public extension Endpoint {
    // Public
    static let config = Endpoint("GET", "/v1/config", requiresAuth: false)

    // Auth
    static func teslaAuthorize(region: String?) -> Endpoint {
        struct B: Encodable { var region: String? }
        return Endpoint("POST", "/v1/auth/tesla/authorize", body: json(B(region: region)), requiresAuth: false)
    }
    static func teslaCallback(code: String, state: String) -> Endpoint {
        struct B: Encodable { var code: String; var state: String }
        return Endpoint("POST", "/v1/auth/tesla/callback", body: json(B(code: code, state: state)), requiresAuth: false)
    }
    static func devLogin(email: String) -> Endpoint {
        struct B: Encodable { var email: String }
        return Endpoint("POST", "/v1/auth/dev/login", body: json(B(email: email)), requiresAuth: false)
    }
    static func ownerTokenLogin(refreshToken: String) -> Endpoint {
        struct B: Encodable { var refreshToken: String }
        return Endpoint("POST", "/v1/auth/dev/tesla-token", body: json(B(refreshToken: refreshToken)), requiresAuth: false)
    }
    static func refresh(refreshToken: String) -> Endpoint {
        struct B: Encodable { var refreshToken: String }
        return Endpoint("POST", "/v1/auth/refresh", body: json(B(refreshToken: refreshToken)), requiresAuth: false)
    }
    static func signOut(refreshToken: String?, everywhere: Bool = false) -> Endpoint {
        struct B: Encodable { var refreshToken: String? }
        return Endpoint("DELETE", "/v1/auth/session",
                        query: everywhere ? [URLQueryItem(name: "all", value: "true")] : [],
                        body: json(B(refreshToken: refreshToken)))
    }
    static let me = Endpoint("GET", "/v1/me")
    static func updatePreferences(_ p: PreferencesPatch) -> Endpoint {
        struct B: Encodable { var preferences: PreferencesPatch }
        return Endpoint("PATCH", "/v1/me", body: json(B(preferences: p)))
    }
    static let deleteAccount = Endpoint("DELETE", "/v1/account")

    // Vehicles
    static let vehicles = Endpoint("GET", "/v1/vehicles")
    static let syncVehicles = Endpoint("POST", "/v1/vehicles/sync", body: json(EmptyBody()))
    static func vehicle(_ id: String) -> Endpoint { Endpoint("GET", "/v1/vehicles/\(pathEscape(id))") }
    static func renameVehicle(_ id: String, name: String) -> Endpoint {
        struct B: Encodable { var displayName: String }
        return Endpoint("PATCH", "/v1/vehicles/\(pathEscape(id))", body: json(B(displayName: name)))
    }
    static func wake(_ id: String) -> Endpoint {
        Endpoint("POST", "/v1/vehicles/\(pathEscape(id))/wake", body: json(EmptyBody()), idempotencyKey: UUID().uuidString)
    }
    static func live(_ id: String) -> Endpoint { Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/live") }
    static func command(_ id: String, name: String, body: Data?, idempotencyKey: String = UUID().uuidString) -> Endpoint {
        Endpoint("POST", "/v1/vehicles/\(pathEscape(id))/commands/\(pathEscape(name))", body: body ?? json(EmptyBody()),
                 idempotencyKey: idempotencyKey)
    }

    // History & analytics
    static func telemetry(_ id: String, from: Date?, to: Date?, resolution: String? = nil) -> Endpoint {
        var q = timeItems(from: from, to: to)
        if let resolution { q.append(URLQueryItem(name: "resolution", value: resolution)) }
        return Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/telemetry", query: q)
    }
    static func trips(_ id: String, limit: Int = 50, cursor: String? = nil, from: Date? = nil, to: Date? = nil) -> Endpoint {
        var q = timeItems(from: from, to: to) + [URLQueryItem(name: "limit", value: String(limit))]
        if let cursor { q.append(URLQueryItem(name: "cursor", value: cursor)) }
        return Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/trips", query: q)
    }
    static func trip(_ id: String, tripID: String) -> Endpoint {
        Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/trips/\(pathEscape(tripID))")
    }
    static func charges(_ id: String, limit: Int = 50, cursor: String? = nil, from: Date? = nil, to: Date? = nil) -> Endpoint {
        var q = timeItems(from: from, to: to) + [URLQueryItem(name: "limit", value: String(limit))]
        if let cursor { q.append(URLQueryItem(name: "cursor", value: cursor)) }
        return Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/charges", query: q)
    }
    static func charge(_ id: String, sessionID: String) -> Endpoint {
        Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/charges/\(pathEscape(sessionID))")
    }
    static func stats(_ id: String, period: StatsPeriod, timeZone: String? = nil, from: Date? = nil, to: Date? = nil) -> Endpoint {
        var q = timeItems(from: from, to: to)
        if let timeZone { q.append(URLQueryItem(name: "tz", value: timeZone)) }
        return Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/stats/\(period.rawValue)", query: q)
    }
    static func batteryHealth(_ id: String) -> Endpoint { Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/battery/health") }
    static func export(_ id: String, kind: String) -> Endpoint {
        Endpoint("GET", "/v1/vehicles/\(pathEscape(id))/export", query: [URLQueryItem(name: "kind", value: kind)])
    }

    // Places, rules, push, events, billing
    static let geofences = Endpoint("GET", "/v1/geofences")
    static func createGeofence(_ g: GeofenceInput) -> Endpoint {
        Endpoint("POST", "/v1/geofences", body: json(g), idempotencyKey: UUID().uuidString)
    }
    static func updateGeofence(_ id: String, _ g: GeofenceInput) -> Endpoint {
        Endpoint("PATCH", "/v1/geofences/\(pathEscape(id))", body: json(g))
    }
    static func deleteGeofence(_ id: String) -> Endpoint { Endpoint("DELETE", "/v1/geofences/\(pathEscape(id))") }
    static let rules = Endpoint("GET", "/v1/rules")
    static func saveRules(_ rules: [Rule]) -> Endpoint {
        struct B: Encodable { var rules: [Rule] }
        return Endpoint("PUT", "/v1/rules", body: json(B(rules: rules)))
    }
    static func registerDevice(token: String, environment: String) -> Endpoint {
        struct B: Encodable { var token: String; var environment: String; var platform = "ios" }
        return Endpoint("POST", "/v1/push/devices", body: json(B(token: token, environment: environment)))
    }
    static func deleteDevice(token: String) -> Endpoint { Endpoint("DELETE", "/v1/push/devices/\(pathEscape(token))") }
    static func events(vehicleID: String? = nil, limit: Int = 50, cursor: String? = nil) -> Endpoint {
        var q = [URLQueryItem(name: "limit", value: String(limit))]
        if let vehicleID { q.append(URLQueryItem(name: "vehicle_id", value: vehicleID)) }
        if let cursor { q.append(URLQueryItem(name: "cursor", value: cursor)) }
        return Endpoint("GET", "/v1/events", query: q)
    }
    static let billingPlan = Endpoint("GET", "/v1/billing/plan")
}
