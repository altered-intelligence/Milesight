import Foundation

/// Chart-ready bucketed telemetry for Swift Charts (AnalyticsKit).
public struct TelemetryBucket: Identifiable, Sendable {
    public let id = UUID()
    public let timestamp: Date
    public let energyKWh: Double
    public let distanceKm: Double
    public let efficiencyWhKm: Double

    public init(timestamp: Date, energyKWh: Double, distanceKm: Double, efficiencyWhKm: Double) {
        self.timestamp = timestamp
        self.energyKWh = energyKWh
        self.distanceKm = distanceKm
        self.efficiencyWhKm = efficiencyWhKm
    }
}

/// Placeholder summarizer; real aggregation comes from the server rollups.
public enum TelemetrySummarizer {
    public static func dailyBuckets(from points: [(Date, Double, Double)]) -> [TelemetryBucket] {
        var acc: [Date: (kwh: Double, km: Double)] = [:]
        for (ts, kwh, km) in points {
            let day = Calendar.current.startOfDay(for: ts)
            let cur = acc[day] ?? (0, 0)
            acc[day] = (cur.kwh + kwh, cur.km + km)
        }
        return acc
            .map { day, v in
                TelemetryBucket(timestamp: day, energyKWh: v.kwh, distanceKm: v.km,
                                efficiencyWhKm: v.km > 0 ? v.kwh * 1000 / v.km : 0)
            }
            .sorted { $0.timestamp < $1.timestamp }
    }
}
