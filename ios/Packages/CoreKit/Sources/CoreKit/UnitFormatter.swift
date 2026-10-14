import Foundation

/// Formats SI values from the API in the user's preferred units.
public struct UnitFormatter: Sendable {
    public var preferences: Preferences
    public var locale: Locale

    public init(preferences: Preferences = Preferences(), locale: Locale = .current) {
        self.preferences = preferences
        self.locale = locale
    }

    public static let kmPerMile = 1.609344

    // MARK: conversions

    /// Distance in the display unit.
    public func distanceValue(_ km: Double) -> Double { preferences.usesMiles ? km / Self.kmPerMile : km }
    public var distanceUnit: String { preferences.usesMiles ? "mi" : "km" }
    public var speedUnit: String { preferences.usesMiles ? "mph" : "km/h" }
    public var efficiencyUnit: String { preferences.usesMiles ? "Wh/mi" : "Wh/km" }
    public var temperatureUnit: String { preferences.usesFahrenheit ? "°F" : "°C" }

    public func temperatureValue(_ c: Double) -> Double { preferences.usesFahrenheit ? c * 9 / 5 + 32 : c }
    /// Converts a display-unit temperature back to °C (for commands).
    public func celsius(fromDisplay v: Double) -> Double { preferences.usesFahrenheit ? (v - 32) * 5 / 9 : v }
    public func efficiencyValue(_ whKm: Double) -> Double { preferences.usesMiles ? whKm * Self.kmPerMile : whKm }

    // MARK: strings

    private func number(_ v: Double, digits: Int) -> String {
        let f = NumberFormatter()
        f.locale = locale
        f.numberStyle = .decimal
        f.minimumFractionDigits = digits
        f.maximumFractionDigits = digits
        return f.string(from: NSNumber(value: v)) ?? String(format: "%.\(digits)f", v)
    }

    public func distance(_ km: Double, digits: Int? = nil) -> String {
        let v = distanceValue(km)
        let d = digits ?? (v < 10 ? 1 : 0)
        return "\(number(v, digits: d)) \(distanceUnit)"
    }

    public func speed(_ kph: Double) -> String { "\(number(distanceValue(kph), digits: 0)) \(speedUnit)" }

    public func temperature(_ c: Double, digits: Int = 0) -> String { "\(number(temperatureValue(c), digits: digits))\(temperatureUnit)" }

    public func efficiency(_ whKm: Double) -> String { "\(number(efficiencyValue(whKm), digits: 0)) \(efficiencyUnit)" }

    public func energy(_ kwh: Double) -> String { "\(number(kwh, digits: kwh < 10 ? 2 : 1)) kWh" }

    public func power(_ kw: Double) -> String { "\(number(kw, digits: kw < 10 ? 1 : 0)) kW" }

    public func percent(_ v: Int) -> String { "\(v)%" }

    public func money(_ amount: Double, currency: String? = nil) -> String {
        let f = NumberFormatter()
        f.locale = locale
        f.numberStyle = .currency
        f.currencyCode = currency ?? preferences.currency
        return f.string(from: NSNumber(value: amount)) ?? "\(String(format: "%.2f", amount)) \(currency ?? preferences.currency)"
    }

    public func duration(seconds: Int) -> String {
        let h = seconds / 3600, m = (seconds % 3600) / 60
        if h > 0 { return "\(h)h \(String(format: "%02d", m))m" }
        return "\(max(1, Int((Double(seconds) / 60).rounded()))) min"
    }

    public func relative(_ date: Date, now: Date = Date()) -> String {
        let s = now.timeIntervalSince(date)
        if s < 45 { return "just now" }
        if s < 3600 { return "\(Int(s / 60)) min ago" }
        if s < 86400 { return "\(Int(s / 3600)) h ago" }
        let f = DateFormatter()
        f.locale = locale
        f.dateStyle = .medium
        f.timeStyle = .short
        return f.string(from: date)
    }
}
