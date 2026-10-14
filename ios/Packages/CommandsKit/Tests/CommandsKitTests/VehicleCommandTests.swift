import XCTest
import CoreKit
@testable import CommandsKit

final class VehicleCommandTests: XCTestCase {
    private func json(_ c: VehicleCommand) -> [String: Any] {
        (try? JSONSerialization.jsonObject(with: c.body ?? Data())) as? [String: Any] ?? [:]
    }

    /// Bodies must match docs/api.md ("Commands") key for key.
    func testBodiesMatchContract() {
        XCTAssertEqual(json(.climate(on: true, tempC: 21.5)) as NSDictionary, ["on": true, "temp_c": 21.5])
        XCTAssertEqual(json(.climate(on: false, tempC: 21.5)) as NSDictionary, ["on": false])
        XCTAssertEqual(json(.charging(.setLimit(85))) as NSDictionary, ["action": "set_limit", "limit_pct": 85])
        XCTAssertEqual(json(.charging(.setAmps(16))) as NSDictionary, ["action": "set_amps", "amps": 16])
        XCTAssertEqual(json(.windows(vent: false)) as NSDictionary, ["action": "close"])
        XCTAssertEqual(json(.sentry(on: true)) as NSDictionary, ["on": true])
        XCTAssertEqual(json(.lock) as NSDictionary, [:])
        XCTAssertEqual(VehicleCommand.chargePort(open: false).name, "charge_port_close")
        XCTAssertEqual(VehicleCommand.flashLights.name, "flash_lights")
    }

    func testOptimisticEffects() {
        var s = LiveState(vehicleId: "v", state: "online",
                          battery: Battery(percent: 50, rangeKm: 250, chargeLimitPct: 80, chargeState: "stopped", pluggedIn: true),
                          climate: Climate(on: false, insideTempC: 20, outsideTempC: 10, driverSetC: 20, passengerSetC: 20,
                                           preconditioning: false, defrostOn: false, fanStatus: 0),
                          closures: Closures(locked: true, doorsOpen: false, windowsOpen: false, frunkOpen: false,
                                             trunkOpen: false, sentryMode: false, userPresent: false),
                          updatedAt: Date())
        VehicleCommand.unlock.applyOptimistically(&s)
        VehicleCommand.climate(on: true, tempC: 22).applyOptimistically(&s)
        VehicleCommand.charging(.start).applyOptimistically(&s)
        VehicleCommand.charging(.setLimit(90)).applyOptimistically(&s)
        XCTAssertEqual(s.closures?.locked, false)
        XCTAssertEqual(s.climate?.on, true)
        XCTAssertEqual(s.climate?.driverSetC, 22)
        XCTAssertEqual(s.battery.chargeState, "charging")
        XCTAssertEqual(s.battery.chargeLimitPct, 90)
    }
}
