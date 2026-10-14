import Foundation

/// Errors surfaced by the API client. `code` values match docs/api.md.
public enum MilesightError: Error, Sendable, Equatable, LocalizedError {
    /// Session is gone (refresh failed): the app must sign in again.
    case signedOut
    /// Tesla access expired: run "Sign in with Tesla" again.
    case teslaReauthRequired
    /// The car refused a command (HTTP 409 with a CommandResult body).
    case commandRefused(CommandResult)
    /// Any other server error.
    case server(status: Int, code: String, message: String, requestId: String?)
    /// Transport-level failure (offline, timeout, TLS).
    case network(String)
    case decoding(String)
    case invalidURL

    public var errorDescription: String? {
        switch self {
        case .signedOut: return "You've been signed out. Please sign in again."
        case .teslaReauthRequired: return "Tesla access expired. Sign in with Tesla again."
        case .commandRefused(let r): return "The car declined: \(Self.humanReason(r.reason))"
        case .server(_, let code, let message, _):
            switch code {
            case "vehicle_unavailable": return "The car is asleep or offline and didn't respond."
            case "rate_limited", "tesla_rate_limited": return "Too many requests. Try again in a moment."
            default: return message.isEmpty ? "Something went wrong (\(code))." : message
            }
        case .network(let m): return "Network problem: \(m)"
        case .decoding: return "The server sent something unexpected. Please update the app."
        case .invalidURL: return "Invalid server address."
        }
    }

    public var code: String? {
        if case .server(_, let code, _, _) = self { return code }
        return nil
    }

    static func humanReason(_ reason: String?) -> String {
        switch reason {
        case "in_motion": return "the car is moving."
        case "not_plugged_in": return "the car isn't plugged in."
        case "complete": return "charging is already complete."
        case "is_charging": return "it's already charging."
        case "not_charging": return "it isn't charging."
        case "cable connected": return "the charge cable is connected."
        case .some(let r) where !r.isEmpty: return r.replacingOccurrences(of: "_", with: " ") + "."
        default: return "no reason given."
        }
    }
}
