import XCTest
import CoreKit
@testable import AnalyticsKit

final class SeriesTests: XCTestCase {
    private func bucket(_ day: Int, km: Double, kwh: Double, eff: Double?) -> StatsBucket {
        let start = Date(timeIntervalSince1970: TimeInterval(day * 86400))
        return StatsBucket(start: start, end: start.addingTimeInterval(86400), distanceKm: km, driveTimeS: 0, trips: km > 0 ? 1 : 0,
                           energyUsedKwh: kwh, efficiencyWhKm: eff, energyAddedKwh: 0, charges: 0, chargingCost: 1.5,
                           drainKm: 0, drainKwh: 0.2, idleHours: 10)
    }

    func testSeriesConvertsUnitsAndSkipsEmptyEfficiency() {
        let stats = StatsResponse(period: "daily", timeZone: "UTC", currency: "USD", from: Date(), to: Date(),
                                  buckets: [bucket(0, km: 16.09344, kwh: 2.4, eff: 149.1), bucket(1, km: 0, kwh: 0, eff: nil)],
                                  totals: bucket(0, km: 16.09344, kwh: 2.4, eff: 149.1))
        let miles = UnitFormatter(preferences: Preferences(), locale: Locale(identifier: "en_US"))
        let dist = stats.series(.distance, formatter: miles)
        XCTAssertEqual(dist.count, 2)
        XCTAssertEqual(dist[0].value, 10, accuracy: 1e-9)
        let eff = stats.series(.efficiency, formatter: miles)
        XCTAssertEqual(eff.count, 1, "days without driving have no efficiency")
        XCTAssertEqual(eff[0].value, 149.1 * 1.609344, accuracy: 1e-6)
        XCTAssertEqual(StatsMetric.cost.total(stats.totals, miles, currency: "USD"), "$1.50")
    }
}
