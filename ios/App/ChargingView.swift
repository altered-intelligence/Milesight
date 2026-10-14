import SwiftUI
import MapKit
import Charts
import CoreKit
import AnalyticsKit

/// Charging sessions with cost, newest first.
@MainActor
struct ChargingView: View {
    @Environment(AppModel.self) private var model
    @State private var sessions: [ChargeSession] = []
    @State private var cursor: String?
    @State private var loading = false
    @State private var loadedFor: String?

    var body: some View {
        let f = model.formatter
        List {
            if let v = model.selectedVehicle, let s = model.vehicles.state(for: v.id), s.battery.pluggedIn {
                Section("Now") {
                    HStack {
                        BatteryRing(percent: s.battery.percent, limit: s.battery.chargeLimitPct, charging: s.isCharging, size: 64)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(s.isCharging ? "Charging" : "Plugged in").font(.headline)
                            if let c = s.charging, s.isCharging {
                                Text("\(f.power(c.powerKw)) · +\(f.energy(c.energyAddedKwh))").font(.subheadline)
                                Text("\(f.duration(seconds: c.minutesToLimit * 60)) to \(s.battery.chargeLimitPct)%")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            if !sessions.isEmpty {
                let totals = Summaries.charges(sessions)
                Section {
                    HStack {
                        StatTile(title: "Sessions", value: "\(totals.count)")
                        StatTile(title: "Energy", value: f.energy(totals.energyKwh))
                        StatTile(title: "Cost", value: f.money(totals.cost))
                    }
                }
            }
            Section {
                ForEach(sessions) { c in
                    NavigationLink(value: Route.charge(c.id)) { ChargeRow(session: c, f: f) }
                        .onAppear { if c.id == sessions.last?.id { Task { await loadMore() } } }
                }
                if loading { HStack { Spacer(); ProgressView(); Spacer() } }
            }
        }
        .overlay {
            if sessions.isEmpty && !loading {
                ContentUnavailableView("No charging yet", systemImage: "bolt.car",
                                       description: Text("Charging sessions are recorded automatically."))
            }
        }
        .navigationTitle("Charging")
        .toolbar {
            ToolbarItem(placement: .principal) { VehiclePicker() }
            ToolbarItem(placement: .topBarTrailing) { ExportButton(kind: "charges") }
        }
        .refreshable { await reload() }
        .task(id: model.selectedVehicle?.id) {
            if loadedFor != model.selectedVehicle?.id { await reload() }
        }
        .onChange(of: model.vehicles.activeCharge[model.selectedVehicle?.id ?? ""]?.id) { old, new in
            if old != nil && new == nil { Task { await reload() } }
        }
    }

    private func reload() async {
        guard let v = model.selectedVehicle else { return }
        loading = true
        defer { loading = false }
        do {
            let page: Page<ChargeSession> = try await model.api.send(.charges(v.id, limit: 30))
            sessions = page.items
            cursor = page.nextCursor
            loadedFor = v.id
        } catch {
            model.show(error)
        }
    }

    private func loadMore() async {
        guard let v = model.selectedVehicle, let c = cursor, !loading else { return }
        loading = true
        defer { loading = false }
        do {
            let page: Page<ChargeSession> = try await model.api.send(.charges(v.id, limit: 30, cursor: c))
            sessions += page.items
            cursor = page.nextCursor
        } catch {
            model.show(error)
        }
    }
}

struct ChargeRow: View {
    let session: ChargeSession
    let f: UnitFormatter

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: session.fastCharger ? "bolt.circle.fill" : "powerplug.fill")
                .foregroundStyle(session.fastCharger ? .yellow : Theme.charging)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 3) {
                Text(session.place?.isEmpty == false ? session.place! : (session.fastCharger ? "Fast charging" : "Charging"))
                    .font(.subheadline.weight(.semibold))
                Text(session.startedAt.formatted(date: .abbreviated, time: .shortened))
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 3) {
                Text("+\(f.energy(session.energyAddedKwh))").font(.metric(15))
                Text("\(session.startBatteryPct)→\(session.endBatteryPct)%"
                     + (session.cost.map { " · " + f.money($0, currency: session.currency) } ?? ""))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

@MainActor
struct ChargeDetailView: View {
    @Environment(AppModel.self) private var model
    let vehicleID: String
    let sessionID: String
    @State private var detail: ChargeDetail?

    var body: some View {
        let f = model.formatter
        ScrollView {
            if let d = detail {
                let s = d.session
                VStack(spacing: 16) {
                    Card {
                        HStack {
                            StatTile(title: "Added", value: f.energy(s.energyAddedKwh))
                            StatTile(title: "Battery", value: "\(s.startBatteryPct)→\(s.endBatteryPct)%")
                            StatTile(title: "Cost", value: s.cost.map { f.money($0, currency: s.currency) } ?? "—")
                        }
                        HStack {
                            StatTile(title: "Duration", value: f.duration(seconds: s.durationS))
                            StatTile(title: "Peak", value: f.power(s.maxPowerKw))
                            StatTile(title: "Average", value: f.power(s.avgPowerKw))
                        }
                        HStack {
                            StatTile(title: "Range added", value: f.distance(max(0, s.endRangeKm - s.startRangeKm)))
                            StatTile(title: "Type", value: s.fastCharger ? "DC fast" : "AC")
                            StatTile(title: "Ended", value: endReason(s))
                        }
                    }
                    if d.curve.count > 1 {
                        Card {
                            Text("Charging curve").font(.headline)
                            Chart {
                                ForEach(Curves.power(d)) { p in
                                    LineMark(x: .value("Time", p.date), y: .value("kW", p.value), series: .value("Series", "Power"))
                                        .foregroundStyle(Theme.charging)
                                }
                            }
                            .chartYAxisLabel("kW")
                            .frame(height: 180)
                            Chart(Curves.battery(d)) { p in
                                LineMark(x: .value("Time", p.date), y: .value("%", p.value))
                                    .foregroundStyle(Theme.accent)
                            }
                            .chartYScale(domain: 0...100)
                            .chartYAxisLabel("Battery %")
                            .frame(height: 140)
                        }
                    }
                    if let loc = s.location {
                        let coord = CLLocationCoordinate2D(latitude: loc.lat, longitude: loc.lon)
                        Map(initialPosition: .region(MKCoordinateRegion(center: coord, latitudinalMeters: 800, longitudinalMeters: 800))) {
                            Marker(s.place ?? "Charger", systemImage: "bolt.fill", coordinate: coord).tint(Theme.charging)
                        }
                        .frame(height: 180)
                        .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
                    }
                }
                .padding()
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .navigationTitle(detail.map { $0.session.startedAt.formatted(date: .abbreviated, time: .shortened) } ?? "Charge")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            do {
                detail = try await model.api.send(.charge(vehicleID, sessionID: sessionID))
            } catch {
                model.show(error)
            }
        }
    }

    private func endReason(_ s: ChargeSession) -> String {
        switch s.endReason {
        case "complete": return "Complete"
        case "stopped": return "Stopped"
        case "unplugged": return "Unplugged"
        case "asleep": return "Car slept"
        default: return s.inProgress ? "Charging" : "—"
        }
    }
}
