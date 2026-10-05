import XCTest
@testable import CoreKit

final class APIRouterTests: XCTestCase {
    func testListVehiclesPath() {
        let base = URL(string: "https://api.example.com")!
        XCTAssertEqual(APIRouter.listVehicles.url(base: base)?.path, "/v1/vehicles")
    }

    func testLiveStatePath() {
        let base = URL(string: "https://api.example.com")!
        XCTAssertEqual(APIRouter.liveState(vehicleID: "abc").url(base: base)?.path, "/v1/vehicles/abc/live")
    }

    func testModelRoundTrip() throws {
        let battery = Battery(percent: 80, rangeKm: 420.0, chargeState: "charging",
                              chargePort: "open", chargingAmps: 32, chargeLimitPct: 90)
        let state = LiveState(vehicleId: "abc", battery: battery, location: nil, climate: nil,
                              locks: nil, software: "2025.4", odometerM: 12345.0, updatedAt: .now)
        let data = try JSONEncoder().encode(state)
        let decoded = try JSONDecoder().decode(LiveState.self, from: data)
        XCTAssertEqual(decoded.battery.percent, 80)
        XCTAssertEqual(decoded.vehicleId, "abc")
    }
}
