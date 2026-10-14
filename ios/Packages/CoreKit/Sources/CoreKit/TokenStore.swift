import Foundation
#if canImport(Security)
import Security
#endif

/// Persists the session (access + refresh token) between launches.
public protocol TokenStore: Sendable {
    func load() -> Session?
    func save(_ session: Session?)
}

/// Keeps the session in memory only (tests, previews, Linux).
public final class InMemoryTokenStore: TokenStore, @unchecked Sendable {
    private let lock = NSLock()
    private var session: Session?

    public init(_ session: Session? = nil) { self.session = session }

    public func load() -> Session? {
        lock.lock(); defer { lock.unlock() }
        return session
    }

    public func save(_ session: Session?) {
        lock.lock(); defer { lock.unlock() }
        self.session = session
    }
}

#if canImport(Security)
/// Stores the session in the Keychain (this device only, after first unlock so
/// background refresh works).
public final class KeychainTokenStore: TokenStore, @unchecked Sendable {
    private let service: String
    private let account: String

    public init(service: String = "com.milesight.session", account: String = "default") {
        self.service = service
        self.account = account
    }

    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    public func load() -> Session? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return try? MilesightJSON.decoder.decode(Session.self, from: data)
    }

    public func save(_ session: Session?) {
        SecItemDelete(query as CFDictionary)
        guard let session, let data = try? MilesightJSON.encoder.encode(session) else { return }
        var q = query
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }
}
#endif
