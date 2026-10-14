import SwiftUI
import CoreKit

@MainActor
struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var homeRate = ""
    @State private var fastRate = ""
    @State private var confirmDelete = false
    @State private var renaming = ""
    @State private var revokeURL: URL?

    var body: some View {
        let p = model.preferences
        NavigationStack {
            Form {
                if let v = model.selectedVehicle, v.isOwner {
                    Section("Vehicle") {
                        TextField("Name", text: $renaming)
                            .onSubmit {
                                let name = renaming
                                Task {
                                    do { try await model.vehicles.rename(v.id, to: name) } catch { model.show(error) }
                                }
                            }
                        LabeledContent("Model", value: [v.model, v.trim].compactMap { $0 }.joined(separator: " "))
                        LabeledContent("VIN", value: v.vin).font(.footnote.monospaced())
                        Button("Refresh vehicle list from Tesla") { Task { await model.vehicles.syncWithTesla() } }
                    }
                }
                Section("Units") {
                    Picker("Distance", selection: binding(p.distanceUnit) { PreferencesPatch(distanceUnit: $0) }) {
                        Text("Miles").tag("mi")
                        Text("Kilometers").tag("km")
                    }
                    Picker("Temperature", selection: binding(p.temperatureUnit) { PreferencesPatch(temperatureUnit: $0) }) {
                        Text("°F").tag("f")
                        Text("°C").tag("c")
                    }
                }
                Section {
                    Picker("Currency", selection: binding(p.currency) { PreferencesPatch(currency: $0) }) {
                        ForEach(Self.currencies(including: p.currency), id: \.self) { Text($0).tag($0) }
                    }
                    rateField("Home (AC) rate / kWh", text: $homeRate) { PreferencesPatch(energyRatePerKwh: $0) }
                    rateField("Fast charging rate / kWh", text: $fastRate) { PreferencesPatch(fastChargeRatePerKwh: $0) }
                    NavigationLink("Places and their rates") { PlacesView() }
                } header: {
                    Text("Charging costs")
                } footer: {
                    Text("Costs use a place's own rate when you're charging there.")
                }
                Section("Account") {
                    LabeledContent("Signed in as", value: model.user?.email ?? "—")
                    LabeledContent("Plan", value: (model.user?.plan ?? "free").capitalized)
                    LabeledContent("Server", value: model.serverURL.host ?? model.serverURL.absoluteString)
                    Button("Sign out") {
                        Task {
                            await model.signOut()
                            dismiss()
                        }
                    }
                    Button("Delete account and data", role: .destructive) { confirmDelete = true }
                }
                if let revokeURL {
                    Section {
                        Link("Revoke Milesight's access at Tesla", destination: revokeURL)
                    }
                }
                Section {
                    Text("Milesight is not affiliated with Tesla, Inc. Tesla and its marks belong to Tesla, Inc.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .onAppear {
                homeRate = String(format: "%.2f", p.energyRatePerKwh)
                fastRate = String(format: "%.2f", p.fastChargeRatePerKwh)
                renaming = model.selectedVehicle?.name ?? ""
            }
            .confirmationDialog("Delete your Milesight account?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete everything", role: .destructive) {
                    Task {
                        do {
                            let r = try await model.deleteAccount()
                            if let s = r.revokeTeslaAccessUrl, let u = URL(string: s) { await UIApplication.shared.open(u) }
                            dismiss()
                        } catch {
                            model.show(error)
                        }
                    }
                }
            } message: {
                Text("This permanently deletes your account, vehicles' history, places and alerts from Milesight.")
            }
        }
    }

    private func binding(_ current: String, _ patch: @escaping (String) -> PreferencesPatch) -> Binding<String> {
        Binding(get: { current }, set: { new in Task { await model.updatePreferences(patch(new)) } })
    }

    private func rateField(_ title: String, text: Binding<String>, _ patch: @escaping (Double) -> PreferencesPatch) -> some View {
        HStack {
            Text(title)
            Spacer()
            TextField("0.00", text: text)
                .keyboardType(.decimalPad)
                .multilineTextAlignment(.trailing)
                .frame(width: 90)
                .onSubmit { commit(text.wrappedValue, patch) }
                .onChange(of: text.wrappedValue) { _, v in
                    // Commit once the value parses; the server validates the range.
                    if Double(v.replacingOccurrences(of: ",", with: ".")) != nil { commit(v, patch) }
                }
        }
    }

    private func commit(_ text: String, _ patch: (Double) -> PreferencesPatch) {
        guard let v = Double(text.replacingOccurrences(of: ",", with: ".")), v >= 0, v <= 10 else { return }
        let p = patch(v)
        Task { await model.updatePreferences(p) }
    }

    static func currencies(including current: String) -> [String] {
        var list = ["USD", "CAD", "EUR", "GBP", "AUD", "NZD", "CHF", "NOK", "SEK", "DKK", "JPY", "CNY", "KRW", "MXN", "AED"]
        if !list.contains(current) { list.insert(current, at: 0) }
        return list
    }
}
