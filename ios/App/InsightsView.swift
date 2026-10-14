import SwiftUI
import Charts
import CoreKit
import AnalyticsKit

/// Day/week/month dashboards: distance, energy, efficiency, cost, drain.
@MainActor
struct InsightsView: View {
    @Environment(AppModel.self) private var model
    @State private var period: StatsPeriod = .daily
    @State private var stats: StatsResponse?
    @State private var loading = false

    var body: some View {
        let f = model.formatter
        ScrollView {
            VStack(spacing: 16) {
                Picker("Period", selection: $period) {
                    Text("Days").tag(StatsPeriod.daily)
                    Text("Weeks").tag(StatsPeriod.weekly)
                    Text("Months").tag(StatsPeriod.monthly)
                }
                .pickerStyle(.segmented)

                if let s = stats {
                    totals(s, f)
                    ForEach([StatsMetric.distance, .efficiency, .energy, .cost, .drain]) { metric in
                        chart(metric, s, f)
                    }
                    NavigationLink(value: Route.batteryHealth) {
                        Card {
                            HStack {
                                Label("Battery health", systemImage: "battery.100percent").font(.headline)
                                Spacer()
                                Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                            }
                        }
                    }
                    .buttonStyle(.plain)
                } else if loading {
                    ProgressView().padding(.top, 60)
                }
            }
            .padding()
        }
        .navigationTitle("Insights")
        .toolbar { ToolbarItem(placement: .principal) { VehiclePicker() } }
        .refreshable { await load() }
        .task(id: "\(model.selectedVehicle?.id ?? "")|\(period.rawValue)|\(model.preferences.timeZone)") { await load() }
    }

    private func load() async {
        guard let v = model.selectedVehicle else { return }
        loading = true
        defer { loading = false }
        do {
            stats = try await model.api.send(.stats(v.id, period: period, timeZone: TimeZone.current.identifier))
        } catch {
            model.show(error)
        }
    }

    private func totals(_ s: StatsResponse, _ f: UnitFormatter) -> some View {
        let t = s.totals
        return Card {
            Text(rangeTitle).font(.headline)
            HStack {
                StatTile(title: "Distance", value: f.distance(t.distanceKm, digits: 0))
                StatTile(title: "Drives", value: "\(t.trips)")
                StatTile(title: "Efficiency", value: t.efficiencyWhKm.map(f.efficiency) ?? "—")
            }
            HStack {
                StatTile(title: "Energy used", value: f.energy(t.energyUsedKwh))
                StatTile(title: "Charged", value: f.energy(t.energyAddedKwh))
                StatTile(title: "Cost", value: f.money(t.chargingCost, currency: s.currency))
            }
            HStack {
                StatTile(title: "Idle drain", value: f.energy(t.drainKwh))
                StatTile(title: "Drain / day", value: t.idleHours > 1 ? f.energy(t.drainKwh / t.idleHours * 24) : "—")
                StatTile(title: "Drive time", value: f.duration(seconds: t.driveTimeS))
            }
        }
    }

    private var rangeTitle: String {
        switch period {
        case .daily: return "Last 30 days"
        case .weekly: return "Last 12 weeks"
        case .monthly: return "Last 12 months"
        }
    }

    private func unit(for period: StatsPeriod) -> Calendar.Component {
        switch period {
        case .daily: return .day
        case .weekly: return .weekOfYear
        case .monthly: return .month
        }
    }

    @ViewBuilder
    private func chart(_ metric: StatsMetric, _ s: StatsResponse, _ f: UnitFormatter) -> some View {
        let points = s.series(metric, formatter: f)
        if points.contains(where: { $0.value > 0 }) {
            Card {
                HStack {
                    Text(metric.title).font(.headline)
                    Spacer()
                    Text(metric.total(s.totals, f, currency: s.currency)).font(.metric(15)).foregroundStyle(.secondary)
                }
                Chart(points) { p in
                    if metric == .efficiency {
                        LineMark(x: .value("Date", p.date, unit: unit(for: period)), y: .value(metric.title, p.value))
                            .interpolationMethod(.monotone)
                        PointMark(x: .value("Date", p.date, unit: unit(for: period)), y: .value(metric.title, p.value))
                            .symbolSize(18)
                    } else {
                        BarMark(x: .value("Date", p.date, unit: unit(for: period)), y: .value(metric.title, p.value))
                            .cornerRadius(3)
                    }
                }
                .foregroundStyle(color(metric))
                .chartYAxisLabel(metric.unit(f, currency: s.currency))
                .frame(height: 170)
            }
        }
    }

    private func color(_ m: StatsMetric) -> Color {
        switch m {
        case .distance, .trips: return Theme.accent
        case .efficiency: return .blue
        case .energy: return .purple
        case .cost: return .orange
        case .drain: return .indigo
        }
    }
}

@MainActor
struct BatteryHealthView: View {
    @Environment(AppModel.self) private var model
    let vehicleID: String
    @State private var health: BatteryHealth?

    var body: some View {
        let f = model.formatter
        ScrollView {
            if let h = health {
                VStack(spacing: 16) {
                    Card {
                        HStack(alignment: .firstTextBaseline) {
                            Text(h.degradationPct.map { String(format: "%.1f%%", $0) } ?? "—").font(.metric(44, weight: .bold))
                            Text("estimated degradation").foregroundStyle(.secondary)
                        }
                        HStack {
                            StatTile(title: "Full range now", value: h.fullRangeKm.map { f.distance($0, digits: 0) } ?? "—")
                            StatTile(title: "When new", value: f.distance(h.nominalFullRangeKm, digits: 0))
                        }
                        HStack {
                            StatTile(title: "Usable capacity", value: h.estimatedCapacityKwh.map(f.energy) ?? "—")
                            StatTile(title: "Nominal", value: f.energy(h.nominalCapacityKwh))
                        }
                        Text("Estimated from \(h.samples) charging sessions. Needs charges of at least 15 % to be accurate; values firm up over a few weeks.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    let range = Curves.fullRange(h, formatter: f)
                    if range.count > 1 {
                        Card {
                            Text("Full range over time (\(f.distanceUnit))").font(.headline)
                            Chart(range) { p in
                                LineMark(x: .value("Month", p.date, unit: .month), y: .value("Range", p.value))
                                PointMark(x: .value("Month", p.date, unit: .month), y: .value("Range", p.value))
                            }
                            .chartYScale(domain: .automatic(includesZero: false))
                            .frame(height: 180)
                        }
                    }
                }
                .padding()
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .navigationTitle("Battery health")
        .task {
            do {
                health = try await model.api.send(.batteryHealth(vehicleID))
            } catch {
                model.show(error)
            }
        }
    }
}
