import Foundation
import CoreKit

/// One plotted value (Swift Charts friendly).
public struct ChartPoint: Identifiable, Hashable, Sendable {
    public var id: Date { date }
    public let date: Date
    public let value: Double

    public init(date: Date, value: Double) {
        self.date = date
        self.value = value
    }
}

/// Metrics available on the analytics dashboard.
public enum StatsMetric: String, CaseIterable, Identifiable, Sendable {
    case distance, energy, efficiency, cost, drain, trips

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .distance: return "Distance"
        case .energy: return "Energy used"
        case .efficiency: return "Efficiency"
        case .cost: return "Charging cost"
        case .drain: return "Idle drain"
        case .trips: return "Trips"
        }
    }

    /// Unit label in the user's preferences.
    public func unit(_ f: UnitFormatter, currency: String) -> String {
        switch self {
        case .distance: return f.distanceUnit
        case .energy, .drain: return "kWh"
        case .efficiency: return f.efficiencyUnit
        case .cost: return currency
        case .trips: return "trips"
        }
    }

    /// Value of one bucket in display units (nil when not meaningful, e.g. no driving).
    public func value(_ b: StatsBucket, _ f: UnitFormatter) -> Double? {
        switch self {
        case .distance: return f.distanceValue(b.distanceKm)
        case .energy: return b.energyUsedKwh
        case .efficiency: return b.efficiencyWhKm.map(f.efficiencyValue)
        case .cost: return b.chargingCost
        case .drain: return b.drainKwh
        case .trips: return Double(b.trips)
        }
    }

    /// Formatted total for the summary row.
    public func total(_ t: StatsBucket, _ f: UnitFormatter, currency: String) -> String {
        switch self {
        case .distance: return f.distance(t.distanceKm)
        case .energy: return f.energy(t.energyUsedKwh)
        case .efficiency: return t.efficiencyWhKm.map(f.efficiency) ?? "—"
        case .cost: return f.money(t.chargingCost, currency: currency)
        case .drain: return f.energy(t.drainKwh)
        case .trips: return "\(t.trips)"
        }
    }
}

public extension StatsResponse {
    /// Points for a metric; buckets without a meaningful value are skipped.
    func series(_ metric: StatsMetric, formatter: UnitFormatter) -> [ChartPoint] {
        buckets.compactMap { b in metric.value(b, formatter).map { ChartPoint(date: b.start, value: $0) } }
    }
}

/// Series for detail screens.
public enum Curves {
    /// Charging power (kW) over time.
    public static func power(_ d: ChargeDetail) -> [ChartPoint] {
        d.curve.map { ChartPoint(date: $0.time, value: $0.powerKw) }
    }

    /// State of charge (%) over time.
    public static func battery(_ d: ChargeDetail) -> [ChartPoint] {
        d.curve.map { ChartPoint(date: $0.time, value: $0.batteryPct) }
    }

    /// Charging power against state of charge (the classic DC taper curve).
    public static func powerBySoC(_ d: ChargeDetail) -> [(soc: Double, kw: Double)] {
        d.curve.map { (soc: $0.batteryPct, kw: $0.powerKw) }.sorted { $0.soc < $1.soc }
    }

    /// Speed along a trip in display units.
    public static func speed(_ d: TripDetail, formatter f: UnitFormatter) -> [ChartPoint] {
        d.route.map { ChartPoint(date: $0.time, value: f.distanceValue($0.speedKph)) }
    }

    /// Battery along a trip.
    public static func battery(_ d: TripDetail) -> [ChartPoint] {
        d.route.map { ChartPoint(date: $0.time, value: $0.batteryPct) }
    }

    /// Monthly battery-health history in display units (full range) or kWh.
    public static func capacity(_ h: BatteryHealth) -> [ChartPoint] {
        h.history.compactMap { p in p.capacityKwh.map { ChartPoint(date: p.month, value: $0) } }
    }

    public static func fullRange(_ h: BatteryHealth, formatter f: UnitFormatter) -> [ChartPoint] {
        h.history.compactMap { p in p.fullRangeKm.map { ChartPoint(date: p.month, value: f.distanceValue($0)) } }
    }
}

/// Lightweight aggregations used by list headers.
public enum Summaries {
    public struct TripTotals: Sendable, Equatable {
        public var count: Int
        public var distanceKm: Double
        public var energyKwh: Double
        public var efficiencyWhKm: Double?
    }

    public static func trips(_ trips: [Trip]) -> TripTotals {
        let done = trips.filter { !$0.inProgress }
        let km = done.reduce(0) { $0 + $1.distanceKm }
        let kwh = done.reduce(0) { $0 + $1.energyUsedKwh }
        return TripTotals(count: done.count, distanceKm: km, energyKwh: kwh, efficiencyWhKm: km >= 1 ? kwh * 1000 / km : nil)
    }

    public struct ChargeTotals: Sendable, Equatable {
        public var count: Int
        public var energyKwh: Double
        public var cost: Double
    }

    public static func charges(_ sessions: [ChargeSession]) -> ChargeTotals {
        let done = sessions.filter { !$0.inProgress }
        return ChargeTotals(count: done.count, energyKwh: done.reduce(0) { $0 + $1.energyAddedKwh },
                            cost: done.reduce(0) { $0 + ($1.cost ?? 0) })
    }
}
