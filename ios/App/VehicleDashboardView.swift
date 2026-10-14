import SwiftUI
import MapKit
import CoreKit
import CommandsKit

@MainActor
struct VehicleDashboardView: View {
    @Environment(AppModel.self) private var model
    @State private var showSettings = false
    @State private var showClimate = false

    var body: some View {
        Group {
            if let v = model.selectedVehicle {
                content(v)
            } else if model.vehicles.isLoading {
                ProgressView()
            } else {
                ContentUnavailableView {
                    Label("No vehicles", systemImage: "car")
                } description: {
                    Text("Vehicles on your Tesla account appear here.")
                } actions: {
                    Button("Check again") { Task { await model.vehicles.syncWithTesla() } }
                }
            }
        }
        .toolbar {
            ToolbarItem(placement: .principal) { VehiclePicker() }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showSettings = true } label: { Image(systemName: "gearshape") }
                    .accessibilityLabel("Settings")
            }
        }
        .navigationTitle(model.vehicles.vehicles.count > 1 ? "" : (model.selectedVehicle?.name ?? "Milesight"))
        .navigationBarTitleDisplayMode(.inline)
        .sheet(isPresented: $showSettings) { SettingsView() }
        .sheet(isPresented: $showClimate) {
            if let v = model.selectedVehicle { ClimateSheet(vehicleID: v.id).presentationDetents([.medium]) }
        }
        .onChange(of: model.commands.lastError) { _, e in
            if let e { model.banner = e.errorDescription; model.commands.lastError = nil }
        }
    }

    @ViewBuilder
    private func content(_ v: Vehicle) -> some View {
        let s = model.vehicles.state(for: v.id)
        let f = model.formatter
        ScrollView {
            VStack(spacing: 16) {
                hero(v, s, f)
                if let trip = model.vehicles.activeTrip[v.id] { liveTripCard(trip, f) }
                if let s, s.battery.pluggedIn { chargingCard(v, s, f) }
                if let sw = s?.software, sw.updateAvailable { updateCard(sw) }
                actions(v, s)
                if let s { climateCard(s, f) }
                if let loc = s?.location { mapCard(loc, v) }
                detailsCard(v, s, f)
            }
            .padding()
        }
        .refreshable {
            await model.vehicles.refresh()
            await model.vehicles.refreshLive(v.id)
        }
    }

    // MARK: Sections

    private func hero(_ v: Vehicle, _ s: LiveState?, _ f: UnitFormatter) -> some View {
        Card {
            HStack(alignment: .center, spacing: 20) {
                BatteryRing(percent: s?.battery.percent ?? 0, limit: s?.battery.chargeLimitPct, charging: s?.isCharging ?? false)
                VStack(alignment: .leading, spacing: 8) {
                    StateBadge(state: v.state, syncStatus: v.syncStatus)
                    Text(f.distance(s?.battery.rangeKm ?? 0, digits: 0))
                        .font(.metric(34, weight: .bold))
                    Text(statusLine(v, s, f))
                        .font(.subheadline).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    if v.state == VehicleStateValue.asleep, v.isOwner {
                        Button {
                            Task {
                                do { try await model.vehicles.wake(v.id) } catch { model.show(error) }
                            }
                        } label: { Label("Wake", systemImage: "sun.max") }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                    }
                }
                Spacer(minLength: 0)
            }
            if let connection = model.vehicles.connection[v.id], case .retrying = connection {
                Label("Reconnecting…", systemImage: "arrow.triangle.2.circlepath").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func statusLine(_ v: Vehicle, _ s: LiveState?, _ f: UnitFormatter) -> String {
        guard let s else { return "Waiting for first update" }
        var parts: [String] = []
        if s.isDriving, let d = s.drive {
            parts.append("Driving · \(f.speed(d.speedKph))")
        } else if s.isCharging {
            parts.append("Charging to \(s.battery.chargeLimitPct)%")
        } else if let place = s.location?.place, !place.isEmpty {
            parts.append("Parked at \(place)")
        } else {
            parts.append("Parked")
        }
        if let c = s.closures { parts.append(c.locked ? "Locked" : "Unlocked") }
        let when = v.lastSeen ?? s.updatedAt
        parts.append("Updated \(f.relative(when))")
        return parts.joined(separator: " · ")
    }

    private func liveTripCard(_ t: Trip, _ f: UnitFormatter) -> some View {
        Card {
            Label("Drive in progress", systemImage: "car.side.fill").font(.headline).foregroundStyle(Theme.accent)
            HStack {
                StatTile(title: "Distance", value: f.distance(t.distanceKm))
                StatTile(title: "Time", value: f.duration(seconds: t.durationS))
                StatTile(title: "Max speed", value: f.speed(t.maxSpeedKph))
            }
        }
    }

    private func chargingCard(_ v: Vehicle, _ s: LiveState, _ f: UnitFormatter) -> some View {
        let ch = s.charging
        return Card {
            HStack {
                Label(chargeTitle(s), systemImage: s.isCharging ? "bolt.fill" : "powerplug.fill")
                    .font(.headline).foregroundStyle(s.isCharging ? Theme.charging : .primary)
                Spacer()
                if let ch, ch.fastCharger { Text("DC").font(.caption.bold()).padding(4).background(.yellow.opacity(0.3), in: Capsule()) }
            }
            if s.isCharging, let ch {
                HStack {
                    StatTile(title: "Power", value: f.power(ch.powerKw))
                    StatTile(title: "Added", value: f.energy(ch.energyAddedKwh))
                    StatTile(title: "To \(s.battery.chargeLimitPct)%", value: f.duration(seconds: ch.minutesToLimit * 60))
                }
                ProgressView(value: Double(s.battery.percent), total: Double(max(s.battery.chargeLimitPct, 1)))
                    .tint(Theme.charging)
            }
            if v.isOwner {
                HStack {
                    if s.isCharging {
                        commandButton("Stop charging", .charging(.stop), v)
                    } else if s.battery.chargeState != ChargeStateValue.complete {
                        commandButton("Start charging", .charging(.start), v)
                    }
                    Spacer()
                    NavigationLink(value: Route.controls) { Text("Charge settings") }
                }
                .font(.subheadline)
            }
        }
    }

    private func chargeTitle(_ s: LiveState) -> String {
        switch s.battery.chargeState {
        case ChargeStateValue.charging, ChargeStateValue.starting: return "Charging"
        case ChargeStateValue.complete: return "Charge complete"
        case ChargeStateValue.stopped: return "Plugged in · stopped"
        case ChargeStateValue.noPower: return "Plugged in · no power"
        default: return "Plugged in"
        }
    }

    private func updateCard(_ sw: Software) -> some View {
        Card {
            Label("Software update available", systemImage: "arrow.down.circle.fill").font(.headline)
            Text("Version \(sw.updateVersion ?? "") is ready to install from the car's touchscreen. Currently on \(sw.version).")
                .font(.subheadline).foregroundStyle(.secondary)
        }
    }

    private func actions(_ v: Vehicle, _ s: LiveState?) -> some View {
        let c = model.commands
        let locked = s?.closures?.locked ?? true
        let climateOn = s?.climate?.on ?? false
        let sentry = s?.closures?.sentryMode ?? false
        let canCommand = v.isOwner
        return Card {
            LazyVGrid(columns: Array(repeating: GridItem(.flexible()), count: 4), spacing: 14) {
                ActionButton(title: locked ? "Unlock" : "Lock", systemImage: locked ? "lock.fill" : "lock.open.fill",
                             active: !locked, busy: c.isSending(locked ? .unlock : .lock, on: v.id), disabled: !canCommand) {
                    send(locked ? .unlock : .lock, v)
                }
                ActionButton(title: "Climate", systemImage: "fanblades.fill", active: climateOn,
                             busy: c.isSending(.climate(on: true), on: v.id), disabled: !canCommand) {
                    showClimate = true
                }
                ActionButton(title: "Frunk", systemImage: "shippingbox.fill", active: s?.closures?.frunkOpen ?? false,
                             busy: c.isSending(.frunk, on: v.id), disabled: !canCommand || (s?.closures?.frunkOpen ?? false)) {
                    send(.frunk, v)
                }
                ActionButton(title: "Trunk", systemImage: "suitcase.fill", active: s?.closures?.trunkOpen ?? false,
                             busy: c.isSending(.trunk, on: v.id), disabled: !canCommand) {
                    send(.trunk, v)
                }
                ActionButton(title: "Port", systemImage: "powerplug.fill", active: s?.battery.chargePortOpen ?? false,
                             busy: c.isSending(.chargePort(open: true), on: v.id) || c.isSending(.chargePort(open: false), on: v.id),
                             disabled: !canCommand) {
                    send(.chargePort(open: !(s?.battery.chargePortOpen ?? false)), v)
                }
                ActionButton(title: "Sentry", systemImage: "eye.fill", active: sentry,
                             busy: c.isSending(.sentry(on: !sentry), on: v.id), disabled: !canCommand) {
                    send(.sentry(on: !sentry), v)
                }
                ActionButton(title: "Lights", systemImage: "headlight.high.beam.fill",
                             busy: c.isSending(.flashLights, on: v.id), disabled: !canCommand) {
                    send(.flashLights, v)
                }
                ActionButton(title: "Honk", systemImage: "speaker.wave.2.fill",
                             busy: c.isSending(.honk, on: v.id), disabled: !canCommand) {
                    send(.honk, v)
                }
            }
            if !canCommand {
                Text("You have view-only access to this car.").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func climateCard(_ s: LiveState, _ f: UnitFormatter) -> some View {
        Card {
            HStack {
                Label(s.climate?.on == true ? "Climate on" : "Climate off", systemImage: "thermometer.medium").font(.headline)
                Spacer()
                Button("Adjust") { showClimate = true }.font(.subheadline)
            }
            HStack {
                StatTile(title: "Inside", value: s.climate?.insideTempC.map { f.temperature($0) } ?? "—")
                StatTile(title: "Outside", value: s.climate?.outsideTempC.map { f.temperature($0) } ?? "—")
                StatTile(title: "Set to", value: s.climate.map { f.temperature($0.driverSetC, digits: 1) } ?? "—")
            }
        }
    }

    private func mapCard(_ loc: Location, _ v: Vehicle) -> some View {
        let coord = CLLocationCoordinate2D(latitude: loc.lat, longitude: loc.lon)
        return Card(padding: 0) {
            Map(initialPosition: .region(MKCoordinateRegion(center: coord, latitudinalMeters: 1200, longitudinalMeters: 1200))) {
                Marker(v.name, systemImage: "car.fill", coordinate: coord).tint(Theme.accent)
            }
            .mapStyle(.standard(pointsOfInterest: .excludingAll))
            .frame(height: 200)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
            .allowsHitTesting(false)
            .id("\(loc.lat),\(loc.lon)")
        }
    }

    private func detailsCard(_ v: Vehicle, _ s: LiveState?, _ f: UnitFormatter) -> some View {
        Card {
            HStack {
                StatTile(title: "Odometer", value: f.distance(s?.odometerKm ?? 0, digits: 0))
                StatTile(title: "Software", value: s?.software?.version ?? "—")
            }
            HStack {
                NavigationLink(value: Route.controls) { Label("Controls", systemImage: "slider.horizontal.3") }
                Spacer()
                NavigationLink(value: Route.batteryHealth) { Label("Battery health", systemImage: "battery.100percent") }
            }
            .font(.subheadline)
            Text([v.model, v.trim].compactMap { $0 }.joined(separator: " · ") + " · VIN \(v.vin.suffix(6))")
                .font(.caption).foregroundStyle(.tertiary)
        }
    }

    private func commandButton(_ title: String, _ cmd: VehicleCommand, _ v: Vehicle) -> some View {
        Button {
            send(cmd, v)
        } label: {
            if model.commands.isSending(cmd, on: v.id) { ProgressView() } else { Text(title) }
        }
        .buttonStyle(.bordered)
    }

    private func send(_ cmd: VehicleCommand, _ v: Vehicle) {
        Task { await model.commands.send(cmd, to: v.id) }
    }
}

/// Climate controls in a sheet.
@MainActor
struct ClimateSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let vehicleID: String
    @State private var setpoint: Double = 21

    var body: some View {
        let f = model.formatter
        let s = model.vehicles.state(for: vehicleID)
        let on = s?.climate?.on ?? false
        NavigationStack {
            VStack(spacing: 24) {
                Text(f.temperature(setpoint, digits: f.preferences.usesFahrenheit ? 0 : 1))
                    .font(.metric(56, weight: .bold))
                Stepper("Temperature", value: $setpoint, in: 15...28, step: 0.5).labelsHidden()
                HStack {
                    StatTile(title: "Inside", value: s?.climate?.insideTempC.map { f.temperature($0) } ?? "—")
                    StatTile(title: "Outside", value: s?.climate?.outsideTempC.map { f.temperature($0) } ?? "—")
                }
                Button {
                    Task {
                        if await model.commands.send(.climate(on: true, tempC: setpoint), to: vehicleID) { dismiss() }
                    }
                } label: {
                    Label(on ? "Update temperature" : "Turn on climate", systemImage: "fanblades.fill").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                if on {
                    Button(role: .destructive) {
                        Task { if await model.commands.send(.climate(on: false), to: vehicleID) { dismiss() } }
                    } label: {
                        Text("Turn off climate").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
            }
            .padding()
            .navigationTitle("Climate")
            .navigationBarTitleDisplayMode(.inline)
        }
        .onAppear { setpoint = s?.climate?.driverSetC ?? 21 }
    }

}
