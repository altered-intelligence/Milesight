import SwiftUI
import UserNotifications
import CoreKit
import NotificationsKit

/// Notification history plus access to rules.
@MainActor
struct AlertsView: View {
    @Environment(AppModel.self) private var model
    @State private var events: [Event] = []
    @State private var cursor: String?
    @State private var loading = false
    @State private var permission: UNAuthorizationStatus = .notDetermined

    var body: some View {
        List {
            if permission == .notDetermined || permission == .denied {
                Section {
                    VStack(alignment: .leading, spacing: 8) {
                        Label("Get notified", systemImage: "bell.badge").font(.headline)
                        Text("Charging complete, low battery, left unlocked and more.").font(.subheadline).foregroundStyle(.secondary)
                        if permission == .denied {
                            Button("Open Settings") {
                                if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                            }
                        } else {
                            Button("Turn on notifications") { Task { await requestPermission() } }
                                .buttonStyle(.borderedProminent)
                        }
                    }
                }
            }
            Section {
                NavigationLink(value: Route.rules) { Label("Notification rules", systemImage: "slider.horizontal.3") }
                NavigationLink(value: Route.places) { Label("Places", systemImage: "mappin.and.ellipse") }
            }
            Section("Recent") {
                ForEach(events) { e in
                    Button { open(e) } label: { EventRow(event: e, vehicleName: model.vehicles.vehicle(e.vehicleId)?.name) }
                        .buttonStyle(.plain)
                        .onAppear { if e.id == events.last?.id { Task { await loadMore() } } }
                }
                if loading { HStack { Spacer(); ProgressView(); Spacer() } }
            }
        }
        .overlay {
            if events.isEmpty && !loading {
                ContentUnavailableView("No alerts yet", systemImage: "bell",
                                       description: Text("Charging, drives, low battery and other alerts show up here."))
            }
        }
        .navigationTitle("Alerts")
        .refreshable { await reload() }
        .task {
            permission = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
            await reload()
        }
        .onChange(of: model.vehicles.recentEvents.first?.id) { _, _ in
            if let e = model.vehicles.recentEvents.first, !events.contains(where: { $0.id == e.id }) {
                events.insert(e, at: 0)
            }
        }
    }

    private func requestPermission() async {
        let granted = (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])) ?? false
        permission = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
        if granted { UIApplication.shared.registerForRemoteNotifications() }
    }

    private func open(_ e: Event) {
        if let url = e.deepLink, let link = DeepLink(url: url) { model.handle(link) }
    }

    private func reload() async {
        loading = true
        defer { loading = false }
        do {
            let page: Page<Event> = try await model.api.send(.events(limit: 40))
            events = page.items
            cursor = page.nextCursor
        } catch {
            model.show(error)
        }
    }

    private func loadMore() async {
        guard let c = cursor, !loading else { return }
        loading = true
        defer { loading = false }
        do {
            let page: Page<Event> = try await model.api.send(.events(limit: 40, cursor: c))
            events += page.items
            cursor = page.nextCursor
        } catch {
            model.show(error)
        }
    }
}

struct EventRow: View {
    let event: Event
    let vehicleName: String?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon)
                .foregroundStyle(tint)
                .frame(width: 28)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                Text(event.title).font(.subheadline.weight(.semibold))
                Text(event.body).font(.subheadline).foregroundStyle(.secondary)
                Text(event.occurredAt.formatted(date: .abbreviated, time: .shortened))
                    .font(.caption2).foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }

    private var icon: String {
        switch event.type {
        case "charge_complete": return "bolt.badge.checkmark.fill"
        case "charging_started": return "bolt.fill"
        case "charging_stopped": return "bolt.slash.fill"
        case "low_battery": return "battery.25percent"
        case "left_unlocked": return "lock.open.fill"
        case "left_open": return "door.left.hand.open"
        case "software_update": return "arrow.down.circle.fill"
        case "geofence_arrive": return "mappin.and.ellipse"
        case "geofence_depart": return "arrow.up.right.circle"
        case "trip_complete": return "flag.checkered"
        default: return "bell.fill"
        }
    }

    private var tint: Color {
        switch event.type {
        case "low_battery", "left_unlocked", "left_open", "charging_stopped": return Theme.warning
        case "charge_complete", "charging_started": return Theme.charging
        default: return Theme.accent
        }
    }
}

/// Toggle notification types and tune thresholds.
@MainActor
struct RulesView: View {
    @Environment(AppModel.self) private var model
    @State private var rules: [Rule] = []

    var body: some View {
        Form {
            Section(footer: Text("Alerts always appear in the Alerts tab; these switches control push notifications.")) {
                ForEach($rules) { $rule in
                    VStack(alignment: .leading, spacing: 6) {
                        Toggle(Self.title(rule.type), isOn: $rule.enabled)
                        if rule.enabled, let key = Self.paramKey(rule.type) {
                            Stepper(value: Binding(
                                get: { rule.params?[key] ?? 0 },
                                set: { rule.params = (rule.params ?? [:]).merging([key: $0]) { $1 } }),
                                    in: key == "threshold_pct" ? 5...50 : 1...120,
                                    step: 5) {
                                Text(key == "threshold_pct" ? "Below \(Int(rule.params?[key] ?? 0))%"
                                     : "After \(Int(rule.params?[key] ?? 0)) min")
                                    .font(.subheadline).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
        .navigationTitle("Notification rules")
        .task {
            do {
                let list: RuleList = try await model.api.send(.rules)
                rules = list.rules
            } catch {
                model.show(error)
            }
        }
        .onChange(of: rules) { old, new in
            guard !old.isEmpty, old != new else { return }
            Task {
                do {
                    let saved: RuleList = try await model.api.send(.saveRules(new))
                    if saved.rules != rules { rules = saved.rules }
                } catch {
                    model.show(error)
                }
            }
        }
    }

    static func title(_ type: String) -> String {
        switch type {
        case "charge_complete": return "Charging complete"
        case "charging_started": return "Charging started"
        case "charging_stopped": return "Charging stopped early"
        case "low_battery": return "Low battery"
        case "left_unlocked": return "Left unlocked"
        case "left_open": return "Door, window or trunk left open"
        case "software_update": return "Software update available"
        case "geofence_arrive": return "Arrived at a place"
        case "geofence_depart": return "Left a place"
        case "trip_complete": return "Drive summary"
        default: return type.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }

    /// The tunable parameter (snake_case, as on the wire) for a rule type.
    static func paramKey(_ type: String) -> String? {
        switch type {
        case "low_battery": return "threshold_pct"
        case "left_unlocked", "left_open": return "minutes"
        default: return nil
        }
    }
}
