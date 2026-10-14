import XCTest
@testable import CoreKit
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

final class CodingTests: XCTestCase {
    func testRFC3339AcceptsNanoseconds() throws {
        let nano = try XCTUnwrap(RFC3339.parse("2026-10-14T06:08:55.527374336Z"))
        let plain = try XCTUnwrap(RFC3339.parse("2026-10-14T06:08:55Z"))
        XCTAssertEqual(nano.timeIntervalSince(plain), 0.527, accuracy: 0.001)
        XCTAssertNotNil(RFC3339.parse("2026-10-14T06:08:55.5Z"))
        XCTAssertNil(RFC3339.parse("yesterday"))
    }

    func testEncodingUsesSnakeCaseIncludingDictionaryKeys() throws {
        let rule = Rule(type: "low_battery", enabled: true, params: ["threshold_pct": 15])
        let json = String(decoding: try MilesightJSON.encoder.encode(rule), as: UTF8.self)
        XCTAssertTrue(json.contains("\"threshold_pct\""), json)
        let patch = PreferencesPatch(distanceUnit: "km", energyRatePerKwh: 0.2)
        let pj = String(decoding: try MilesightJSON.encoder.encode(patch), as: UTF8.self)
        XCTAssertTrue(pj.contains("\"energy_rate_per_kwh\"") && !pj.contains("currency"), pj)
    }

    func testEndpointURLs() {
        let base = URL(string: "https://api.example.com")!
        XCTAssertEqual(Endpoint.vehicles.url(base: base)?.absoluteString, "https://api.example.com/v1/vehicles")
        let prefixed = URL(string: "https://example.com/milesight/")!
        XCTAssertEqual(Endpoint.live("abc").url(base: prefixed)?.path, "/milesight/v1/vehicles/abc/live")
        let stats = Endpoint.stats("v1", period: .weekly, timeZone: "America/Chicago").url(base: base)!
        XCTAssertTrue(stats.absoluteString.contains("/stats/weekly?tz=America/Chicago"))
        XCTAssertNotNil(Endpoint.command("v", name: "lock", body: nil).idempotencyKey)
    }

    func testUnitFormatter() {
        let imperial = UnitFormatter(preferences: Preferences(), locale: Locale(identifier: "en_US"))
        XCTAssertEqual(imperial.distance(16.09344), "10 mi")
        XCTAssertEqual(imperial.temperature(21), "70°F")
        XCTAssertEqual(imperial.efficiency(150), "241 Wh/mi")
        XCTAssertEqual(imperial.celsius(fromDisplay: 70), 21.11, accuracy: 0.01)
        var p = Preferences()
        p.distanceUnit = "km"
        p.temperatureUnit = "c"
        let metric = UnitFormatter(preferences: p, locale: Locale(identifier: "en_US"))
        XCTAssertEqual(metric.distance(16.09344), "16 km")
        XCTAssertEqual(metric.speed(88), "88 km/h")
        XCTAssertEqual(metric.duration(seconds: 3720), "1h 02m")
        XCTAssertEqual(metric.duration(seconds: 90), "2 min")
    }
}

/// Scripted transport for exercising the client's auth handling.
final class FakeTransport: HTTPTransport, @unchecked Sendable {
    let lock = NSLock()
    var refreshCalls = 0
    var requests: [String] = []
    var validToken = "fresh"
    var refreshStatus = 200

    static func session(token: String, refresh: String, expiresIn: TimeInterval) -> Data {
        let exp = RFC3339.format(Date().addingTimeInterval(expiresIn))
        let now = RFC3339.format(Date())
        return Data("""
        {"access_token":"\(token)","refresh_token":"\(refresh)","token_type":"Bearer","expires_in":900,"expires_at":"\(exp)",
         "user":{"id":"u1","email":"a@b.c","plan":"free","created_at":"\(now)","preferences":{"distance_unit":"mi",
         "temperature_unit":"f","currency":"USD","energy_rate_per_kwh":0.15,"fast_charge_rate_per_kwh":0.45,"time_zone":"UTC"}}}
        """.utf8)
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url!.path
        let auth = request.value(forHTTPHeaderField: "Authorization")
        lock.lock()
        requests.append("\(request.httpMethod ?? "") \(path)")
        if path == "/v1/auth/refresh" { refreshCalls += 1 }
        let status = refreshStatus
        lock.unlock()
        func respond(_ code: Int, _ body: String) -> (Data, HTTPURLResponse) {
            (Data(body.utf8), HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: nil)!)
        }
        if path == "/v1/auth/refresh" {
            try await Task.sleep(nanoseconds: 50_000_000)
            if status != 200 { return respond(status, #"{"error":{"code":"invalid_refresh_token","message":"x"}}"#) }
            return (Self.session(token: "fresh", refresh: "r2", expiresIn: 900), HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!)
        }
        guard auth == "Bearer \(validToken)" else {
            return respond(401, #"{"error":{"code":"unauthorized","message":"expired"}}"#)
        }
        if path.hasSuffix("/commands/charging") {
            return respond(409, #"{"request_id":"r","command":"charging","status":"failed","reason":"not_plugged_in","woke_up":false,"at":"2026-10-14T06:16:33.5Z"}"#)
        }
        return respond(200, #"{"vehicles":[]}"#)
    }
}

final class APIClientTests: XCTestCase {
    private func client(_ t: FakeTransport) throws -> APIClient {
        let s = try MilesightJSON.decoder.decode(Session.self, from: FakeTransport.session(token: "stale", refresh: "r1", expiresIn: 600))
        return APIClient(baseURL: URL(string: "https://api.example.com")!, tokens: InMemoryTokenStore(s), transport: t)
    }

    func testConcurrent401sShareOneRefresh() async throws {
        let t = FakeTransport()
        let c = try client(t)
        try await withThrowingTaskGroup(of: VehicleList.self) { group in
            for _ in 0..<5 { group.addTask { try await c.send(.vehicles, as: VehicleList.self) } }
            for try await _ in group {}
        }
        XCTAssertEqual(t.refreshCalls, 1, "refresh tokens are single-use; refreshes must be coalesced")
        let token = await c.accessToken
        XCTAssertEqual(token, "fresh")
    }

    func testFailedRefreshSignsOut() async throws {
        let t = FakeTransport()
        t.refreshStatus = 401
        let c = try client(t)
        do {
            _ = try await c.send(.vehicles, as: VehicleList.self)
            XCTFail("expected sign-out")
        } catch let e as MilesightError {
            XCTAssertEqual(e, .signedOut)
        }
        let session = await c.session
        XCTAssertNil(session)
    }

    func testRefusedCommandSurfacesResult() async throws {
        let t = FakeTransport()
        t.validToken = "stale"
        let c = try client(t)
        do {
            try await c.sendVoid(.command("v", name: "charging", body: nil))
            XCTFail("expected refusal")
        } catch MilesightError.commandRefused(let r) {
            XCTAssertEqual(r.reason, "not_plugged_in")
        }
    }
}
