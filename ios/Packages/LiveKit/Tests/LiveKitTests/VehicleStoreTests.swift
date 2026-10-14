import XCTest
import CoreKit
@testable import LiveKit

@MainActor
final class VehicleStoreTests: XCTestCase {
    private func store() -> VehicleStore {
        VehicleStore(api: APIClient(baseURL: URL(string: "https://example.invalid")!, tokens: InMemoryTokenStore()))
    }

    private func state(_ pct: Int, locked: Bool = true, at: TimeInterval) -> LiveState {
        LiveState(vehicleId: "v1", state: "online", battery: Battery(percent: pct, rangeKm: Double(pct) * 5),
                  closures: Closures(locked: locked, doorsOpen: false, windowsOpen: false, frunkOpen: false,
                                     trunkOpen: false, sentryMode: false, userPresent: false),
                  updatedAt: Date(timeIntervalSince1970: at))
    }

    func testStateNeverGoesBackwards() {
        let s = store()
        s.apply(.state(state(70, at: 200)))
        s.apply(.state(state(60, at: 100)))
        XCTAssertEqual(s.state(for: "v1")?.battery.percent, 70)
        s.apply(.state(state(69, at: 300)))
        XCTAssertEqual(s.state(for: "v1")?.battery.percent, 69)
    }

    func testOverlayHoldsUntilRemoved() {
        let s = store()
        s.apply(.state(state(70, locked: true, at: 100)))
        let previous = s.state(for: "v1")
        let token = s.addOverlay("v1") { $0.closures?.locked = false }
        XCTAssertEqual(s.state(for: "v1")?.closures?.locked, false)
        // A stale-but-newer poll still says locked: the overlay keeps the optimistic value.
        s.apply(.state(state(70, locked: true, at: 110)))
        XCTAssertEqual(s.state(for: "v1")?.closures?.locked, false)
        // Command failed: roll back.
        s.removeOverlay("v1", token: token, restoring: previous)
        XCTAssertEqual(s.state(for: "v1")?.closures?.locked, true)
    }

    func testTripAndChargeTracking() throws {
        let s = store()
        let tripJSON = #"{"id":"t","vehicle_id":"v1","started_at":"2026-10-14T06:00:00Z","start_odometer_km":1,"end_odometer_km":2,"distance_km":1,"duration_s":60,"avg_speed_kph":60,"max_speed_kph":70,"start_battery_pct":80,"end_battery_pct":79,"start_range_km":400,"end_range_km":398,"energy_used_kwh":0.2}"#
        let trip = try MilesightJSON.decoder.decode(Trip.self, from: Data(tripJSON.utf8))
        s.apply(.trip(trip))
        XCTAssertNotNil(s.activeTrip["v1"])
        var ended = trip
        ended.endedAt = Date()
        s.apply(.trip(ended))
        XCTAssertNil(s.activeTrip["v1"])
    }
}
