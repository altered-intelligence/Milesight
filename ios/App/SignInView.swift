import SwiftUI
import AuthenticationServices
import CoreKit

@MainActor
struct SignInView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.webAuthenticationSession) private var webAuth

    @State private var email = ""
    @State private var ownerToken = ""
    @State private var serverText = ""
    @State private var region = "na"
    @State private var busy = false
    @State private var error: String?
    @State private var showServer = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 28) {
                    header
                    if let cfg = model.config {
                        methods(cfg)
                    } else if let e = model.configError {
                        Card {
                            Label("Can't reach the server", systemImage: "wifi.exclamationmark").font(.headline)
                            Text(e).font(.subheadline).foregroundStyle(.secondary)
                            Text(model.serverURL.absoluteString).font(.footnote.monospaced()).foregroundStyle(.secondary)
                            Button("Try again") { Task { await model.loadConfig() } }
                        }
                    } else {
                        ProgressView().padding()
                    }
                    if let error {
                        Text(error).font(.footnote).foregroundStyle(Theme.danger).multilineTextAlignment(.center)
                    }
                    serverSection
                    Text("Milesight is not affiliated with Tesla, Inc.")
                        .font(.caption2).foregroundStyle(.tertiary)
                }
                .padding(24)
                .frame(maxWidth: 520)
                .frame(maxWidth: .infinity)
            }
            .disabled(busy)
            .overlay { if busy { ProgressView().controlSize(.large) } }
        }
    }

    private var header: some View {
        VStack(spacing: 10) {
            Image(systemName: "gauge.with.dots.needle.67percent")
                .font(.system(size: 56, weight: .semibold))
                .foregroundStyle(Theme.accent)
            Text("Milesight").font(.largeTitle.bold())
            Text("Live status, trips, charging and insights for your Tesla.")
                .font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .padding(.top, 32)
    }

    @ViewBuilder
    private func methods(_ cfg: ServerConfig) -> some View {
        VStack(spacing: 16) {
            if cfg.teslaOauth {
                Picker("Region", selection: $region) {
                    Text("North America / Asia-Pacific").tag("na")
                    Text("Europe / Middle East / Africa").tag("eu")
                    Text("China").tag("cn")
                }
                .pickerStyle(.menu)
                Button {
                    run { try await model.signInWithTesla(using: webAuth, region: region) }
                } label: {
                    Label("Sign in with Tesla", systemImage: "person.badge.key.fill").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
            }
            if cfg.devLogin {
                Card {
                    Text("Try the demo").font(.headline)
                    Text("Simulated cars with a month of driving and charging history. No Tesla account needed.")
                        .font(.subheadline).foregroundStyle(.secondary)
                    TextField("you@example.com", text: $email)
                        .textContentType(.emailAddress)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .textFieldStyle(.roundedBorder)
                        .onSubmit(demo)
                    Button("Start demo", action: demo)
                        .buttonStyle(.bordered)
                        .disabled(!email.contains("@"))
                }
            }
            if cfg.ownerTokenLogin {
                DisclosureGroup("Developer sign-in (Owner API token)") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Paste a Tesla refresh token. For development only.")
                            .font(.caption).foregroundStyle(.secondary)
                        SecureField("Refresh token", text: $ownerToken).textFieldStyle(.roundedBorder)
                        Button("Sign in") {
                            run { try await model.signInWithOwnerToken(ownerToken) }
                        }
                        .disabled(ownerToken.count < 20)
                    }
                    .padding(.top, 8)
                }
            }
            if !cfg.teslaOauth && !cfg.devLogin && !cfg.ownerTokenLogin {
                Text("This server has no sign-in methods enabled.").foregroundStyle(.secondary)
            }
        }
    }

    private var serverSection: some View {
        DisclosureGroup(isExpanded: $showServer) {
            VStack(alignment: .leading, spacing: 8) {
                TextField("https://api.example.com", text: $serverText)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .textFieldStyle(.roundedBorder)
                Button("Use this server") {
                    Task { await model.setServer(serverText) }
                }
                .disabled(serverText.isEmpty)
            }
            .padding(.top, 8)
        } label: {
            Label("Server: \(model.serverURL.host ?? model.serverURL.absoluteString)", systemImage: "server.rack")
                .font(.footnote)
        }
        .onAppear { serverText = model.serverURL.absoluteString }
    }

    private func demo() {
        let e = email
        run { try await model.signInDemo(email: e) }
    }

    @MainActor
    private func run(_ work: @escaping @MainActor () async throws -> Void) {
        busy = true
        error = nil
        Task { @MainActor in
            defer { busy = false }
            do {
                try await work()
            } catch let e as ASWebAuthenticationSessionError where e.code == .canceledLogin {
                // user closed the sheet
            } catch {
                self.error = (error as? MilesightError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}
