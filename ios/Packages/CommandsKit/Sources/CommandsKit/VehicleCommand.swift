import Foundation
import CoreKit

public enum ChargingAction: Sendable, Hashable {
    case start, stop
    case setAmps(Int)
    case setLimit(Int)
}

/// A remote command (docs/api.md, "Commands").
public enum VehicleCommand: Sendable, Hashable {
    case climate(on: Bool, tempC: Double? = nil)
    case lock, unlock
    case frunk, trunk
    case chargePort(open: Bool)
    case charging(ChargingAction)
    case honk, flashLights
    case sentry(on: Bool)
    case windows(vent: Bool)

    /// Path segment for POST /v1/vehicles/{id}/commands/{name}.
    public var name: String {
        switch self {
        case .climate: return "climate"
        case .lock: return "lock"
        case .unlock: return "unlock"
        case .frunk: return "frunk"
        case .trunk: return "trunk"
        case .chargePort(let open): return open ? "charge_port" : "charge_port_close"
        case .charging: return "charging"
        case .honk: return "honk"
        case .flashLights: return "flash_lights"
        case .sentry: return "sentry"
        case .windows: return "windows"
        }
    }

    /// Short label for progress and error messages.
    public var label: String {
        switch self {
        case .climate(let on, _): return on ? "Turning on climate" : "Turning off climate"
        case .lock: return "Locking"
        case .unlock: return "Unlocking"
        case .frunk: return "Opening frunk"
        case .trunk: return "Opening trunk"
        case .chargePort(let open): return open ? "Opening charge port" : "Closing charge port"
        case .charging(.start): return "Starting charge"
        case .charging(.stop): return "Stopping charge"
        case .charging(.setAmps(let a)): return "Setting \(a) A"
        case .charging(.setLimit(let l)): return "Setting limit to \(l)%"
        case .honk: return "Honking"
        case .flashLights: return "Flashing lights"
        case .sentry(let on): return on ? "Enabling Sentry" : "Disabling Sentry"
        case .windows(let vent): return vent ? "Venting windows" : "Closing windows"
        }
    }

    struct Params: Encodable {
        var on: Bool?
        var tempC: Double?
        var action: String?
        var amps: Int?
        var limitPct: Int?
    }

    var params: Params {
        switch self {
        case .climate(let on, let t): return Params(on: on, tempC: on ? t : nil)
        case .sentry(let on): return Params(on: on)
        case .charging(.start): return Params(action: "start")
        case .charging(.stop): return Params(action: "stop")
        case .charging(.setAmps(let a)): return Params(action: "set_amps", amps: a)
        case .charging(.setLimit(let l)): return Params(action: "set_limit", limitPct: l)
        case .windows(let vent): return Params(action: vent ? "vent" : "close")
        default: return Params()
        }
    }

    /// JSON body (snake_case via MilesightJSON).
    public var body: Data? { try? MilesightJSON.encoder.encode(params) }

    /// The state change the user expects, shown immediately (optimistic UI).
    public func applyOptimistically(_ s: inout LiveState) {
        switch self {
        case .climate(let on, let t):
            s.climate?.on = on
            if let t { s.climate?.driverSetC = t; s.climate?.passengerSetC = t }
        case .lock: s.closures?.locked = true
        case .unlock: s.closures?.locked = false
        case .frunk: s.closures?.frunkOpen = true
        case .trunk: s.closures?.trunkOpen.toggle()
        case .chargePort(let open): s.battery.chargePortOpen = open
        case .charging(.start):
            if s.battery.pluggedIn { s.battery.chargeState = ChargeStateValue.charging }
        case .charging(.stop):
            if s.battery.pluggedIn { s.battery.chargeState = ChargeStateValue.stopped }
        case .charging(.setLimit(let l)): s.battery.chargeLimitPct = l
        case .charging(.setAmps(let a)): s.charging?.ampsRequest = a
        case .sentry(let on): s.closures?.sentryMode = on
        case .windows(let vent): s.closures?.windowsOpen = vent
        case .honk, .flashLights: break
        }
    }
}
