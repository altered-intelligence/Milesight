import Foundation

/// JSON coding that matches the server contract (docs/api.md):
/// snake_case keys and RFC 3339 timestamps (the Go server emits up to nanosecond precision).
///
/// Free-form dictionaries (`Event.payload`, `Rule.params`) are normalized to
/// snake_case keys after decoding (see `SnakeCase`), matching the wire format.
public enum MilesightJSON {
    public static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let raw = try container.decode(String.self)
            guard let date = RFC3339.parse(raw) else {
                throw DecodingError.dataCorruptedError(in: container, debugDescription: "Invalid RFC 3339 date: \(raw)")
            }
            return date
        }
        return d
    }()

    public static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(RFC3339.format(date))
        }
        return e
    }()
}

/// RFC 3339 parsing that tolerates any number of fractional-second digits.
public enum RFC3339 {
    private static let withFraction: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private static let lock = NSLock()

    public static func parse(_ s: String) -> Date? {
        lock.lock()
        defer { lock.unlock() }
        if let d = plain.date(from: s) { return d }
        if let d = withFraction.date(from: s) { return d }
        // Normalize the fraction to milliseconds: "…:55.527374336Z" → "…:55.527Z".
        guard let dot = s.firstIndex(of: ".") else { return nil }
        var end = s.index(after: dot)
        while end < s.endIndex, s[end].isNumber { end = s.index(after: end) }
        var digits = String(s[s.index(after: dot)..<end])
        if digits.isEmpty { return nil }
        digits = String((digits + "000").prefix(3))
        let normalized = String(s[..<dot]) + "." + digits + String(s[end...])
        return withFraction.date(from: normalized)
    }

    public static func format(_ d: Date) -> String {
        lock.lock()
        defer { lock.unlock() }
        return withFraction.string(from: d)
    }
}

/// A type-erased JSON value for free-form payloads.
public enum JSONValue: Codable, Sendable, Hashable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case array([JSONValue])
    case object([String: JSONValue])
    case null

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .number(let n): try c.encode(n)
        case .bool(let b): try c.encode(b)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        case .null: try c.encodeNil()
        }
    }

    public var string: String? { if case .string(let s) = self { return s }; return nil }
    public var number: Double? { if case .number(let n) = self { return n }; return nil }
}
