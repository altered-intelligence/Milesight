import SwiftUI
import UserNotifications
import NotificationsKit

/// Bridges UIKit push callbacks into the SwiftUI app.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    var onDeviceToken: ((Data) -> Void)?
    var onOpenLink: ((DeepLink) -> Void)?

    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        onDeviceToken?(deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        // Simulator or missing entitlement: the app works without push.
    }

    // Show alerts while the app is in the foreground.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async
        -> UNNotificationPresentationOptions {
        [.banner, .sound, .list]
    }

    // Tapping a notification opens its deep link.
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let info = response.notification.request.content.userInfo
        if let link = DeepLink(pushPayload: info) {
            await MainActor.run { self.onOpenLink?(link) }
        }
    }
}

@main
struct MilesightApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var model = AppModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .tint(Theme.accent)
                .task {
                    delegate.onDeviceToken = { token in Task { @MainActor in model.didRegisterForPush(token: token) } }
                    delegate.onOpenLink = { link in Task { @MainActor in model.handle(link) } }
                    await model.start()
                }
                .onOpenURL { url in
                    if let link = DeepLink(url: url) { model.handle(link) }
                }
                .onChange(of: scenePhase) { _, phase in
                    // Live streams only while the app is in the foreground.
                    if phase == .background { model.vehicles.stopAll() }
                    if phase == .active, model.phase == .signedIn {
                        model.syncStreaming()
                        Task { await model.vehicles.refresh() }
                    }
                }
        }
    }
}

/// Switches between sign-in and the signed-in app.
struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        Group {
            switch model.phase {
            case .launching:
                ProgressView("Milesight").controlSize(.large)
            case .signedOut:
                SignInView()
            case .signedIn:
                MainTabView()
            }
        }
        .appBanner($model.banner)
    }
}

struct MainTabView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        TabView(selection: $model.selectedTab) {
            NavigationStack(path: $model.carPath) {
                VehicleDashboardView()
                    .navigationDestination(for: Route.self) { RouteView(route: $0) }
            }
            .tabItem { Label("Car", systemImage: "car.fill") }
            .tag(AppTab.car)

            NavigationStack(path: $model.drivesPath) {
                TripsView()
                    .navigationDestination(for: Route.self) { RouteView(route: $0) }
            }
            .tabItem { Label("Drives", systemImage: "road.lanes") }
            .tag(AppTab.drives)

            NavigationStack(path: $model.chargingPath) {
                ChargingView()
                    .navigationDestination(for: Route.self) { RouteView(route: $0) }
            }
            .tabItem { Label("Charging", systemImage: "bolt.car.fill") }
            .tag(AppTab.charging)

            NavigationStack(path: $model.insightsPath) {
                InsightsView()
                    .navigationDestination(for: Route.self) { RouteView(route: $0) }
            }
            .tabItem { Label("Insights", systemImage: "chart.xyaxis.line") }
            .tag(AppTab.insights)

            NavigationStack(path: $model.alertsPath) {
                AlertsView()
                    .navigationDestination(for: Route.self) { RouteView(route: $0) }
            }
            .tabItem { Label("Alerts", systemImage: "bell.badge.fill") }
            .tag(AppTab.alerts)
        }
        // Stream the selected car while signed in and visible.
        .task(id: model.selectedVehicle?.id) { model.syncStreaming() }
    }
}

/// Resolves a Route for whichever tab pushed it.
struct RouteView: View {
    @Environment(AppModel.self) private var model
    let route: Route

    var body: some View {
        if let v = model.selectedVehicle {
            switch route {
            case .trip(let id): TripDetailView(vehicleID: v.id, tripID: id)
            case .charge(let id): ChargeDetailView(vehicleID: v.id, sessionID: id)
            case .batteryHealth: BatteryHealthView(vehicleID: v.id)
            case .controls: ControlsView(vehicleID: v.id)
            case .places: PlacesView()
            case .rules: RulesView()
            }
        } else {
            ContentUnavailableView("No vehicle", systemImage: "car")
        }
    }
}

/// Toolbar menu to switch vehicles (only shown with more than one).
struct VehiclePicker: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if model.vehicles.vehicles.count > 1 {
            Menu {
                ForEach(model.vehicles.vehicles) { v in
                    Button {
                        model.selectedVehicleID = v.id
                    } label: {
                        if v.id == model.selectedVehicle?.id {
                            Label(v.name, systemImage: "checkmark")
                        } else {
                            Text(v.name)
                        }
                    }
                }
            } label: {
                HStack(spacing: 4) {
                    Text(model.selectedVehicle?.name ?? "Vehicle").font(.headline)
                    Image(systemName: "chevron.down").font(.caption.bold())
                }
            }
            .accessibilityLabel("Switch vehicle")
        }
    }
}
