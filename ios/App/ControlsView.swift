import SwiftUI
import CoreKit
import CommandsKit

/// Full control surface: charging, climate, windows, security.
@MainActor
struct ControlsView: View {
    @Environment(AppModel.self) private var model
    let vehicleID: String
    @State private var limit: Double = 80
    @State private var amps: Int = 32
    @State private var editingLimit = false

    var body: some View {
        let s = model.vehicles.state(for: vehicleID)
        let f = model.formatter
        let owner = model.vehicles.vehicle(vehicleID)?.isOwner ?? false
        Form {
            Section("Charging") {
                VStack(alignment: .leading) {
                    HStack {
                        Text("Charge limit")
                        Spacer()
                        Text("\(Int(limit))%").font(.metric(17)).foregroundStyle(.secondary)
                    }
                    Slider(value: $limit, in: 50...100, step: 1, onEditingChanged: { editing in
                        editingLimit = editing
                        if !editing { send(.charging(.setLimit(Int(limit)))) }
                    })
                }
                if s?.battery.pluggedIn == true, s?.charging?.fastCharger == false {
                    Stepper(value: $amps, in: 5...48) {
                        HStack {
                            Text("Charge current")
                            Spacer()
                            Text("\(amps) A").font(.metric(17)).foregroundStyle(.secondary)
                        }
                    }
                    Button("Apply \(amps) A") { send(.charging(.setAmps(amps))) }
                }
                if s?.battery.pluggedIn == true {
                    if s?.isCharging == true {
                        Button("Stop charging", role: .destructive) { send(.charging(.stop)) }
                    } else {
                        Button("Start charging") { send(.charging(.start)) }
                    }
                }
                Button((s?.battery.chargePortOpen ?? false) ? "Close charge port" : "Open charge port") {
                    send(.chargePort(open: !(s?.battery.chargePortOpen ?? false)))
                }
            }
            Section("Climate") {
                LabeledContent("Inside", value: s?.climate?.insideTempC.map { f.temperature($0) } ?? "—")
                LabeledContent("Outside", value: s?.climate?.outsideTempC.map { f.temperature($0) } ?? "—")
                Toggle("Climate", isOn: Binding(
                    get: { s?.climate?.on ?? false },
                    set: { on in send(.climate(on: on, tempC: s?.climate?.driverSetC)) }))
            }
            Section("Security") {
                Toggle("Locked", isOn: Binding(
                    get: { s?.closures?.locked ?? true },
                    set: { locked in send(locked ? .lock : .unlock) }))
                Toggle("Sentry Mode", isOn: Binding(
                    get: { s?.closures?.sentryMode ?? false },
                    set: { on in send(.sentry(on: on)) }))
            }
            Section("Windows & trunks") {
                Button("Vent windows") { send(.windows(vent: true)) }
                Button("Close windows") { send(.windows(vent: false)) }
                Button("Open frunk") { send(.frunk) }
                Button((s?.closures?.trunkOpen ?? false) ? "Close trunk" : "Open trunk") { send(.trunk) }
            }
            if let c = s?.closures {
                Section("Status") {
                    LabeledContent("Doors", value: c.doorsOpen ? "Open" : "Closed")
                    LabeledContent("Windows", value: c.windowsOpen ? "Open" : "Closed")
                    LabeledContent("Frunk", value: c.frunkOpen ? "Open" : "Closed")
                    LabeledContent("Trunk", value: c.trunkOpen ? "Open" : "Closed")
                }
            }
        }
        .disabled(!owner || model.commands.isBusy(vehicleID))
        .overlay(alignment: .bottom) {
            if model.commands.isBusy(vehicleID) {
                Label("Sending…", systemImage: "antenna.radiowaves.left.and.right")
                    .padding(10).background(.regularMaterial, in: Capsule()).padding()
            }
        }
        .navigationTitle("Controls")
        .onAppear {
            limit = Double(s?.battery.chargeLimitPct ?? 80)
            amps = max(5, s?.charging?.ampsRequest ?? 32)
        }
        .onChange(of: s?.battery.chargeLimitPct) { _, new in
            if !editingLimit, let new { limit = Double(new) }
        }
    }

    private func send(_ cmd: VehicleCommand) {
        Task { await model.commands.send(cmd, to: vehicleID) }
    }
}
