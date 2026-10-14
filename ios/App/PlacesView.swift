import SwiftUI
import MapKit
import CoreKit

/// User places (geofences): name drives and charges, set per-place energy rates,
/// and power arrive/leave alerts.
@MainActor
struct PlacesView: View {
    @Environment(AppModel.self) private var model
    @State private var places: [Geofence] = []
    @State private var editing: Geofence?
    @State private var creating = false

    var body: some View {
        let f = model.formatter
        List {
            ForEach(places) { p in
                Button { editing = p } label: {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(p.name).font(.headline)
                        Text("\(Int(p.radiusM)) m radius" + (p.energyRatePerKwh.map { " · \(f.money($0))/kWh" } ?? ""))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
            }
            .onDelete { idx in
                let gone = idx.map { places[$0] }
                places.remove(atOffsets: idx)
                Task {
                    for g in gone { try? await model.api.sendVoid(.deleteGeofence(g.id)) }
                }
            }
        }
        .overlay {
            if places.isEmpty {
                ContentUnavailableView("No places", systemImage: "mappin.and.ellipse",
                                       description: Text("Add Home or Work to label drives and charges and set a home energy rate."))
            }
        }
        .navigationTitle("Places")
        .toolbar {
            Button { creating = true } label: { Image(systemName: "plus") }.accessibilityLabel("Add place")
        }
        .task { await reload() }
        .sheet(item: $editing) { g in PlaceEditor(existing: g) { await reload() } }
        .sheet(isPresented: $creating) { PlaceEditor(existing: nil) { await reload() } }
    }

    private func reload() async {
        do {
            let list: GeofenceList = try await model.api.send(.geofences)
            places = list.geofences
        } catch {
            model.show(error)
        }
    }
}

@MainActor
struct PlaceEditor: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let existing: Geofence?
    let onSave: () async -> Void

    @State private var name = ""
    @State private var radius: Double = 150
    @State private var rateText = ""
    @State private var position: MapCameraPosition = .automatic
    @State private var center: CLLocationCoordinate2D?
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Name (e.g. Home)", text: $name)
                }
                Section("Location") {
                    ZStack {
                        Map(position: $position) {
                            if let c = center {
                                MapCircle(center: c, radius: radius).foregroundStyle(Theme.accent.opacity(0.2)).stroke(Theme.accent, lineWidth: 2)
                            }
                        }
                        .onMapCameraChange(frequency: .continuous) { ctx in center = ctx.region.center }
                        Image(systemName: "mappin").font(.title).foregroundStyle(Theme.accent).offset(y: -12)
                    }
                    .frame(height: 240)
                    .listRowInsets(EdgeInsets())
                    Button("Use the car's location") { useCarLocation() }
                        .disabled(model.selectedVehicle.flatMap { model.vehicles.state(for: $0.id)?.location } == nil)
                    VStack(alignment: .leading) {
                        Text("Radius: \(Int(radius)) m").font(.subheadline)
                        Slider(value: $radius, in: 50...1000, step: 25)
                    }
                }
                Section {
                    TextField("Energy rate per kWh (optional)", text: $rateText).keyboardType(.decimalPad)
                } footer: {
                    Text("Charging here is priced at this rate (in \(model.preferences.currency)) instead of your default.")
                }
                if let error { Text(error).foregroundStyle(Theme.danger).font(.footnote) }
            }
            .navigationTitle(existing == nil ? "New place" : "Edit place")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { await save() } }
                        .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || center == nil || saving)
                }
            }
            .onAppear(perform: setup)
        }
    }

    private func setup() {
        if let g = existing {
            name = g.name
            radius = g.radiusM
            rateText = g.energyRatePerKwh.map { String($0) } ?? ""
            let c = CLLocationCoordinate2D(latitude: g.lat, longitude: g.lon)
            center = c
            position = .region(MKCoordinateRegion(center: c, latitudinalMeters: 1500, longitudinalMeters: 1500))
        } else {
            useCarLocation()
        }
    }

    private func useCarLocation() {
        guard let v = model.selectedVehicle, let loc = model.vehicles.state(for: v.id)?.location else { return }
        let c = CLLocationCoordinate2D(latitude: loc.lat, longitude: loc.lon)
        center = c
        position = .region(MKCoordinateRegion(center: c, latitudinalMeters: 1500, longitudinalMeters: 1500))
    }

    private func save() async {
        guard let c = center else { return }
        let rate = Double(rateText.replacingOccurrences(of: ",", with: "."))
        if !rateText.isEmpty && rate == nil {
            error = "Enter the rate as a number, like 0.12."
            return
        }
        saving = true
        defer { saving = false }
        let input = GeofenceInput(name: name.trimmingCharacters(in: .whitespaces), lat: c.latitude, lon: c.longitude,
                                  radiusM: radius, energyRatePerKwh: rate, clearEnergyRate: rate == nil && existing != nil ? true : nil)
        do {
            if let g = existing {
                let _: Geofence = try await model.api.send(.updateGeofence(g.id, input))
            } else {
                let _: Geofence = try await model.api.send(.createGeofence(input))
            }
            await onSave()
            dismiss()
        } catch {
            self.error = (error as? MilesightError)?.errorDescription ?? error.localizedDescription
        }
    }
}
