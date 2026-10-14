import XCTest
@testable import CoreKit

/// Decodes real responses captured from the Go server (backend/cmd/fixtures).
/// If one of these fails, the Swift models and docs/api.md have drifted apart.
final class ContractTests: XCTestCase {
    private func fixture(_ name: String) throws -> Data {
        guard let url = Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "Fixtures") else {
            XCTFail("missing fixture \(name).json")
            throw CocoaError(.fileNoSuchFile)
        }
        return try Data(contentsOf: url)
    }

    private func decode<T: Decodable>(_ type: T.Type, _ name: String) throws -> T {
        try MilesightJSON.decoder.decode(T.self, from: fixture(name))
    }

    func testConfigSessionAndUser() throws {
        let cfg = try decode(ServerConfig.self, "config")
        XCTAssertTrue(cfg.devLogin)
        XCTAssertEqual(cfg.transport, "sim")
        let s = try decode(Session.self, "session")
        XCTAssertEqual(s.tokenType, "Bearer")
        XCTAssertEqual(s.user.email, "fixtures@example.com")
        XCTAssertGreaterThan(s.expiresAt, s.user.createdAt)
        let me = try decode(User.self, "me")
        XCTAssertEqual(me.preferences.currency, "USD")
    }

    func testVehiclesAndLiveState() throws {
        let list = try decode(VehicleList.self, "vehicles")
        XCTAssertEqual(list.vehicles.count, 2)
        let v = list.vehicles[0]
        XCTAssertEqual(v.vin.count, 17)
        XCTAssertTrue(v.isOwner)
        XCTAssertNotNil(v.latest)
        let live = try decode(LiveState.self, "live")
        XCTAssertEqual(live.vehicleId, v.id)
        XCTAssertGreaterThan(live.battery.rangeKm, 0)
        XCTAssertNotNil(live.climate)
        XCTAssertNotNil(live.closures)
        XCTAssertNotNil(live.location)
    }

    func testCommands() throws {
        let ok = try decode(CommandResult.self, "command_result")
        XCTAssertTrue(ok.succeeded)
        let refused = try decode(CommandResult.self, "command_refused")
        XCTAssertFalse(refused.succeeded)
        XCTAssertEqual(refused.reason, "not_plugged_in")
        XCTAssertEqual(MilesightError.commandRefused(refused).errorDescription, "The car declined: the car isn't plugged in.")
    }

    func testHistory() throws {
        let trips = try decode(Page<Trip>.self, "trips_page")
        XCTAssertFalse(trips.items.isEmpty)
        let detail = try decode(TripDetail.self, "trip_detail")
        XCTAssertFalse(detail.route.isEmpty)
        XCTAssertNotNil(detail.trip.endedAt)
        XCTAssertGreaterThan(detail.trip.distanceKm, 0)
        let charges = try decode(Page<ChargeSession>.self, "charges_page")
        XCTAssertFalse(charges.items.isEmpty)
        let cd = try decode(ChargeDetail.self, "charge_detail")
        XCTAssertFalse(cd.curve.isEmpty)
        XCTAssertNotNil(cd.session.cost)
        let stats = try decode(StatsResponse.self, "stats_daily")
        XCTAssertEqual(stats.period, "daily")
        XCTAssertEqual(stats.timeZone, "America/Chicago")
        XCTAssertGreaterThan(stats.totals.trips, 0)
        let health = try decode(BatteryHealth.self, "battery_health")
        XCTAssertGreaterThan(health.nominalCapacityKwh, 0)
        let tele = try decode(TelemetryResponse.self, "telemetry")
        XCTAssertFalse(tele.points.isEmpty)
    }

    func testPlacesRulesEventsErrors() throws {
        let rules = try decode(RuleList.self, "rules")
        XCTAssertEqual(rules.rules.count, 10)
        let low = try XCTUnwrap(rules.rules.first { $0.type == "low_battery" })
        XCTAssertEqual(low.params?["thresholdPct"], 20, "dictionary keys are camelCased by the decoder")
        let g = try decode(Geofence.self, "geofence")
        XCTAssertEqual(g.name, "Home")
        XCTAssertEqual(g.energyRatePerKwh, 0.12)
        let events = try decode(Page<Event>.self, "events_page")
        let e = try XCTUnwrap(events.items.first)
        XCTAssertEqual(e.type, "charge_complete")
        XCTAssertEqual(e.deepLink?.scheme, "milesight")
        let err = try decode(APIErrorEnvelope.self, "error_not_found")
        XCTAssertEqual(err.error.code, "not_found")
        XCTAssertNotNil(err.error.requestId)
    }

    func testWebSocketEnvelope() throws {
        guard case .state(let s) = try LiveMessage.decode(fixture("ws_state")) else {
            return XCTFail("expected a state message")
        }
        XCTAssertFalse(s.vehicleId.isEmpty)
        guard case .unknown(let t) = try LiveMessage.decode(Data(#"{"type":"future_thing","data":{}}"#.utf8)) else {
            return XCTFail("unknown types must be tolerated")
        }
        XCTAssertEqual(t, "future_thing")
    }
}
