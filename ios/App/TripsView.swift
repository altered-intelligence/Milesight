import SwiftUI
import MapKit
import Charts
import CoreKit
import AnalyticsKit

/// Drives: automatically logged trips, newest first.
@MainActor
struct TripsView: View {
    @Environment(AppModel.self) private var model
    @State private var trips: [Trip] = []
    @State private var cursor: String?
    @State private var loading = false
    @State private var loadedFor: String?
    @State private var shareURL: URL?

    var body: some View {
        let f = model.formatter
        List {
            if let v = model.selectedVehicle, let live = model.vehicles.activeTrip[v.id] {
                Section("Now") { TripRow(trip: live, f: f) }
            }
            if !trips.isEmpty {
                let totals = Summaries.trips(trips)
                Section {
                    HStack {
                        StatTile(title: "Drives", value: "\(totals.count)")
                        StatTile(title: "Distance", value: f.distance(totals.distanceKm, digits: 0))
                        StatTile(title: "Efficiency", value: totals.efficiencyWhKm.map(f.efficiency) ?? "—")
                    }
                } footer: {
                    Text("Totals for the drives loaded below.")
                }
            }
            Section {
                ForEach(trips.filter { !$0.inProgress }) { t in
                    NavigationLink(value: Route.trip(t.id)) { TripRow(trip: t, f: f) }
                        .onAppear { if t.id == trips.last?.id { Task { await loadMore() } } }
                }
                if loading { HStack { Spacer(); ProgressView(); Spacer() } }
            }
        }
        .overlay {
            if trips.isEmpty && !loading {
                ContentUnavailableView("No drives yet", systemImage: "road.lanes",
                                       description: Text("Drives are recorded automatically as the car moves."))
            }
        }
        .navigationTitle("Drives")
        .toolbar {
            ToolbarItem(placement: .principal) { VehiclePicker() }
            ToolbarItem(placement: .topBarTrailing) { ExportButton(kind: "trips") }
        }
        .refreshable { await reload() }
        .task(id: model.selectedVehicle?.id) {
            if loadedFor != model.selectedVehicle?.id { await reload() }
        }
        .onChange(of: model.vehicles.activeTrip[model.selectedVehicle?.id ?? ""]?.id) { old, new in
            if old != nil && new == nil { Task { await reload() } } // a drive just ended
        }
    }

    private func reload() async {
        guard let v = model.selectedVehicle else { return }
        cursor = nil
        loading = true
        defer { loading = false }
        do {
            let page: Page<Trip> = try await model.api.send(.trips(v.id, limit: 30))
            trips = page.items
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
            let page: Page<Trip> = try await model.api.send(.trips(v.id, limit: 30, cursor: c))
            trips += page.items
            cursor = page.nextCursor
        } catch {
            model.show(error)
        }
    }
}

struct TripRow: View {
    let trip: Trip
    let f: UnitFormatter

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: trip.inProgress ? "car.side.fill" : "point.topleft.down.to.point.bottomright.curvepath")
                .foregroundStyle(Theme.accent)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.subheadline.weight(.semibold)).lineLimit(1)
                Text(trip.startedAt.formatted(date: .abbreviated, time: .shortened))
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 3) {
                Text(f.distance(trip.distanceKm)).font(.metric(15))
                Text(trip.efficiencyWhKm.map(f.efficiency) ?? f.duration(seconds: trip.durationS))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }

    private var title: String {
        let from = trip.startPlace?.isEmpty == false ? trip.startPlace! : nil
        let to = trip.endPlace?.isEmpty == false ? trip.endPlace! : nil
        switch (from, to) {
        case let (a?, b?): return "\(a) → \(b)"
        case let (nil, b?): return "To \(b)"
        case let (a?, nil): return "From \(a)"
        default: return trip.inProgress ? "Driving now" : "Drive · \(f.duration(seconds: trip.durationS))"
        }
    }
}

@MainActor
struct TripDetailView: View {
    @Environment(AppModel.self) private var model
    let vehicleID: String
    let tripID: String
    @State private var detail: TripDetail?

    var body: some View {
        let f = model.formatter
        ScrollView {
            if let d = detail {
                VStack(spacing: 16) {
                    routeMap(d)
                    Card {
                        HStack {
                            StatTile(title: "Distance", value: f.distance(d.trip.distanceKm))
                            StatTile(title: "Duration", value: f.duration(seconds: d.trip.durationS))
                            StatTile(title: "Avg speed", value: f.speed(d.trip.avgSpeedKph))
                        }
                        HStack {
                            StatTile(title: "Energy", value: f.energy(d.trip.energyUsedKwh))
                            StatTile(title: "Efficiency", value: d.trip.efficiencyWhKm.map(f.efficiency) ?? "—")
                            StatTile(title: "Battery", value: "\(d.trip.startBatteryPct)→\(d.trip.endBatteryPct)%")
                        }
                        HStack {
                            StatTile(title: "Max speed", value: f.speed(d.trip.maxSpeedKph))
                            StatTile(title: "Outside", value: d.trip.outsideTempC.map { f.temperature($0) } ?? "—")
                            StatTile(title: "Range used", value: f.distance(d.trip.startRangeKm - d.trip.endRangeKm))
                        }
                    }
                    let speed = Curves.speed(d, formatter: f)
                    if speed.count > 1 {
                        Card {
                            Text("Speed (\(f.speedUnit))").font(.headline)
                            Chart(speed) { p in
                                AreaMark(x: .value("Time", p.date), y: .value("Speed", p.value))
                                    .foregroundStyle(Theme.accent.opacity(0.25))
                                LineMark(x: .value("Time", p.date), y: .value("Speed", p.value))
                                    .foregroundStyle(Theme.accent)
                            }
                            .frame(height: 160)
                        }
                    }
                }
                .padding()
            } else {
                ProgressView().padding(.top, 80)
            }
        }
        .navigationTitle(detail.map { $0.trip.startedAt.formatted(date: .abbreviated, time: .shortened) } ?? "Drive")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            do {
                detail = try await model.api.send(.trip(vehicleID, tripID: tripID))
            } catch {
                model.show(error)
            }
        }
    }

    @ViewBuilder
    private func routeMap(_ d: TripDetail) -> some View {
        let coords = d.route.map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
        if coords.count > 1 {
            Map(initialPosition: .automatic) {
                MapPolyline(coordinates: coords).stroke(Theme.accent, lineWidth: 5)
                if let first = coords.first { Marker("Start", systemImage: "flag.fill", coordinate: first).tint(.green) }
                if let last = coords.last { Marker("End", systemImage: "flag.checkered", coordinate: last).tint(.red) }
            }
            .frame(height: 260)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        }
    }
}

/// Downloads a CSV export and offers it through the share sheet.
@MainActor
struct ExportButton: View {
    @Environment(AppModel.self) private var model
    let kind: String
    @State private var file: URL?
    @State private var busy = false

    var body: some View {
        if let file {
            ShareLink(item: file) { Image(systemName: "square.and.arrow.up") }
        } else {
            Button {
                Task { await export() }
            } label: {
                if busy { ProgressView() } else { Image(systemName: "square.and.arrow.down") }
            }
            .accessibilityLabel("Export CSV")
            .disabled(model.selectedVehicle == nil)
        }
    }

    private func export() async {
        guard let v = model.selectedVehicle else { return }
        busy = true
        defer { busy = false }
        do {
            let data = try await model.api.sendData(.export(v.id, kind: kind))
            let url = FileManager.default.temporaryDirectory.appendingPathComponent("milesight-\(kind)-\(v.name).csv")
            try data.write(to: url, options: .atomic)
            file = url
        } catch {
            model.show(error)
        }
    }
}
