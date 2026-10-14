import SwiftUI
import UserNotifications
import AuthenticationServices
import CoreKit
import LiveKit
import CommandsKit
import NotificationsKit

/// Tabs of the signed-in app.
enum AppTab: Hashable {
    case car, drives, charging, insights, alerts
}

/// Destinations pushed onto a tab's navigation stack.
enum Route: Hashable {
    case trip(String)
    case charge(String)
    case batteryHealth
    case controls
    case places
    case rules
}

/// App-wide state: server, session, stores and navigation.
@MainActor
@Observable
final class AppModel {
    enum Phase: Equatable { case launching, signedOut, signedIn }

    private(set) var phase: Phase = .launching
    private(set) var serverURL: URL
    private(set) var config: ServerConfig?
    private(set) var configError: String?
    private(set) var user: User?
    private(set) var api: APIClient
    private(set) var vehicles: VehicleStore
    private(set) var commands: CommandCenter

    var selectedVehicleID: String?
    var selectedTab: AppTab = .car
    var carPath: [Route] = []
    var drivesPath: [Route] = []
    var chargingPath: [Route] = []
    var insightsPath: [Route] = []
    var alertsPath: [Route] = []
    var banner: String?

    @ObservationIgnored private var sessionWatch: Task<Void, Never>?
    @ObservationIgnored private var pushToken: Data?

    static let serverKey = "milesight.serverURL"

    init() {
        let url = Self.storedServerURL()
        let api = APIClient(baseURL: url, tokens: KeychainTokenStore())
        let store = VehicleStore(api: api)
        self.serverURL = url
        self.api = api
        self.vehicles = store
        self.commands = CommandCenter(api: api, store: store)
        watchSession()
    }

    // MARK: Derived

    var formatter: UnitFormatter { UnitFormatter(preferences: user?.preferences ?? Preferences()) }
    var preferences: Preferences { user?.preferences ?? Preferences() }

    var selectedVehicle: Vehicle? {
        if let id = selectedVehicleID, let v = vehicles.vehicle(id) { return v }
        return vehicles.vehicles.first
    }

    // MARK: Server

    static func storedServerURL() -> URL {
        if let s = UserDefaults.standard.string(forKey: serverKey), let u = URL(string: s) { return u }
        if let s = Bundle.main.object(forInfoDictionaryKey: "MilesightAPIBaseURL") as? String, let u = URL(string: s) { return u }
        return URL(string: "http://localhost:8080")!
    }

    /// Points the app at another server (signs out of the current one).
    func setServer(_ text: String) async {
        var t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !t.contains("://") { t = "https://" + t }
        guard let url = URL(string: t), url.host != nil else {
            configError = "That doesn't look like a server address."
            return
        }
        await signOut(remote: false)
        UserDefaults.standard.set(url.absoluteString, forKey: Self.serverKey)
        serverURL = url
        api = APIClient(baseURL: url, tokens: KeychainTokenStore())
        vehicles = VehicleStore(api: api)
        commands = CommandCenter(api: api, store: vehicles)
        watchSession()
        await loadConfig()
    }

    func loadConfig() async {
        do {
            config = try await api.send(.config)
            configError = nil
        } catch {
            config = nil
            configError = (error as? MilesightError)?.errorDescription ?? error.localizedDescription
        }
    }

    // MARK: Session lifecycle

    private func watchSession() {
        sessionWatch?.cancel()
        let api = self.api
        sessionWatch = Task { [weak self] in
            for await s in await api.sessionChanges() {
                guard let self else { return }
                if s == nil && self.phase == .signedIn { self.didSignOut(message: "Your session ended. Please sign in again.") }
            }
        }
    }

    /// Called at launch: restore a stored session or show sign-in.
    func start() async {
        await loadConfig()
        if await api.session != nil {
            phase = .signedIn
            await bootstrap()
        } else {
            phase = .signedOut
        }
    }

    private func bootstrap() async {
        do {
            user = try await api.send(.me)
        } catch MilesightError.signedOut {
            didSignOut(message: nil)
            return
        } catch {
            banner = (error as? MilesightError)?.errorDescription
        }
        await vehicles.refresh()
        if selectedVehicleID == nil || vehicles.vehicle(selectedVehicleID!) == nil {
            selectedVehicleID = vehicles.vehicles.first?.id
        }
        // If notifications were allowed earlier, (re)register to get a fresh token.
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        if settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional {
            UIApplication.shared.registerForRemoteNotifications()
        }
        await registerPushIfPossible()
    }

    private func didSignIn(_ s: Session) async {
        await api.setSession(s)
        user = s.user
        phase = .signedIn
        selectedTab = .car
        await bootstrap()
    }

    private func didSignOut(message: String?) {
        vehicles.clear()
        user = nil
        selectedVehicleID = nil
        carPath = []; drivesPath = []; chargingPath = []; insightsPath = []; alertsPath = []
        phase = .signedOut
        banner = message
    }

    func signInDemo(email: String) async throws {
        let s: Session = try await api.send(.devLogin(email: email))
        await didSignIn(s)
    }

    func signInWithOwnerToken(_ token: String) async throws {
        let s: Session = try await api.send(.ownerTokenLogin(refreshToken: token.trimmingCharacters(in: .whitespacesAndNewlines)))
        await didSignIn(s)
    }

    /// Sign in with Tesla: server-issued authorize URL → system browser sheet →
    /// milesight://auth/callback → exchange on the server.
    func signInWithTesla(using session: WebAuthenticationSession, region: String?) async throws {
        let auth: AuthorizeResponse = try await api.send(.teslaAuthorize(region: region))
        guard let url = URL(string: auth.authorizeUrl) else { throw MilesightError.invalidURL }
        let callback = try await session.authenticate(using: url, callbackURLScheme: "milesight",
                                                      preferredBrowserSession: .ephemeral)
        guard case .authCallback(let code, let state, let error)? = DeepLink(url: callback) else {
            throw MilesightError.server(status: 400, code: "bad_callback", message: "Unexpected sign-in response.", requestId: nil)
        }
        if let error {
            throw MilesightError.server(status: 400, code: error, message: "Tesla sign-in was cancelled or denied.", requestId: nil)
        }
        guard let code, let state, state == auth.state else {
            throw MilesightError.server(status: 400, code: "invalid_state", message: "Sign-in didn't complete. Please try again.", requestId: nil)
        }
        let s: Session = try await api.send(.teslaCallback(code: code, state: state))
        await didSignIn(s)
    }

    func signOut(remote: Bool = true) async {
        if remote, let s = await api.session {
            if let token = pushToken { try? await PushRegistrar(api: api, environment: Self.pushEnvironment).unregister(deviceToken: token) }
            try? await api.sendVoid(.signOut(refreshToken: s.refreshToken))
        }
        await api.setSession(nil)
        didSignOut(message: nil)
    }

    func deleteAccount() async throws -> AccountDeletion {
        let result: AccountDeletion = try await api.send(.deleteAccount)
        await api.setSession(nil)
        didSignOut(message: "Your account and data were deleted.")
        return result
    }

    func updatePreferences(_ patch: PreferencesPatch) async {
        do {
            user = try await api.send(.updatePreferences(patch))
        } catch {
            banner = (error as? MilesightError)?.errorDescription ?? error.localizedDescription
        }
    }

    // MARK: Push

    static var pushEnvironment: String {
        #if DEBUG
        return "sandbox"
        #else
        return "production"
        #endif
    }

    func didRegisterForPush(token: Data) {
        pushToken = token
        Task { await registerPushIfPossible() }
    }

    private func registerPushIfPossible() async {
        guard phase == .signedIn, let token = pushToken else { return }
        try? await PushRegistrar(api: api, environment: Self.pushEnvironment).register(deviceToken: token)
    }

    // MARK: Live streaming

    /// Streams only the selected vehicle (one WebSocket at a time).
    func syncStreaming() {
        guard phase == .signedIn, let id = selectedVehicle?.id else { return }
        for v in vehicles.vehicles where v.id != id { vehicles.stopStreaming(v.id) }
        vehicles.startStreaming(id)
    }

    // MARK: Deep links

    func handle(_ link: DeepLink) {
        guard phase == .signedIn, let vid = link.vehicleID else { return }
        selectedVehicleID = vid
        switch link {
        case .vehicle:
            selectedTab = .car
            carPath = []
        case .charging:
            selectedTab = .charging
            chargingPath = []
        case .charge(_, let cid):
            selectedTab = .charging
            chargingPath = [.charge(cid)]
        case .trip(_, let tid):
            selectedTab = .drives
            drivesPath = [.trip(tid)]
        case .authCallback:
            break
        }
    }

    func show(_ error: Error) {
        banner = (error as? MilesightError)?.errorDescription ?? error.localizedDescription
    }
}
