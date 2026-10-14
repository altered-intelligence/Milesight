import SwiftUI
import CoreKit

/// Milesight's small design system: one accent, rounded numerals, quiet cards.
enum Theme {
    static let accent = Color(red: 0.0, green: 0.72, blue: 0.58)
    static let charging = Color(red: 0.2, green: 0.78, blue: 0.35)
    static let warning = Color.orange
    static let danger = Color.red
    static let cardRadius: CGFloat = 18

    static func batteryColor(_ pct: Int, charging: Bool) -> Color {
        if charging { return Theme.charging }
        switch pct {
        case ..<15: return Theme.danger
        case ..<30: return Theme.warning
        default: return Theme.accent
        }
    }
}

extension Font {
    static func metric(_ size: CGFloat, weight: Font.Weight = .semibold) -> Font {
        .system(size: size, weight: weight, design: .rounded).monospacedDigit()
    }
}

/// A rounded card container.
struct Card<Content: View>: View {
    var padding: CGFloat = 16
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) { content }
            .padding(padding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.background.secondary, in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
    }
}

/// Title + value tile.
struct StatTile: View {
    let title: String
    let value: String
    var systemImage: String?
    var tint: Color = .primary

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label {
                Text(title)
            } icon: {
                if let systemImage { Image(systemName: systemImage) }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            Text(value)
                .font(.metric(20))
                .foregroundStyle(tint)
                .lineLimit(1)
                .minimumScaleFactor(0.7)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Circular battery gauge.
struct BatteryRing: View {
    let percent: Int
    var limit: Int?
    var charging = false
    var size: CGFloat = 120

    var body: some View {
        let color = Theme.batteryColor(percent, charging: charging)
        ZStack {
            Circle().stroke(color.opacity(0.15), lineWidth: size * 0.09)
            if let limit {
                Circle()
                    .trim(from: CGFloat(limit) / 100 - 0.003, to: CGFloat(limit) / 100 + 0.003)
                    .stroke(Color.secondary, style: StrokeStyle(lineWidth: size * 0.13, lineCap: .butt))
                    .rotationEffect(.degrees(-90))
            }
            Circle()
                .trim(from: 0, to: CGFloat(max(0, min(100, percent))) / 100)
                .stroke(color, style: StrokeStyle(lineWidth: size * 0.09, lineCap: .round))
                .rotationEffect(.degrees(-90))
                .animation(.easeInOut(duration: 0.6), value: percent)
            VStack(spacing: 0) {
                HStack(alignment: .firstTextBaseline, spacing: 1) {
                    Text("\(percent)").font(.metric(size * 0.3, weight: .bold))
                    Text("%").font(.metric(size * 0.14)).foregroundStyle(.secondary)
                }
                if charging {
                    Image(systemName: "bolt.fill").font(.system(size: size * 0.12)).foregroundStyle(Theme.charging)
                }
            }
        }
        .frame(width: size, height: size)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Battery \(percent) percent\(charging ? ", charging" : "")")
    }
}

/// Online / asleep / offline pill.
struct StateBadge: View {
    let state: String
    var syncStatus: String?

    var body: some View {
        let (text, color, icon): (String, Color, String) = {
            switch state {
            case VehicleStateValue.online: return ("Online", Theme.accent, "dot.radiowaves.left.and.right")
            case VehicleStateValue.asleep: return ("Asleep", .indigo, "moon.zzz.fill")
            case VehicleStateValue.offline: return ("Offline", .gray, "wifi.slash")
            default: return ("Unknown", .gray, "questionmark.circle")
            }
        }()
        Label(syncStatus == "error" ? "Needs attention" : text, systemImage: syncStatus == "error" ? "exclamationmark.triangle.fill" : icon)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .foregroundStyle(syncStatus == "error" ? Theme.warning : color)
            .background((syncStatus == "error" ? Theme.warning : color).opacity(0.14), in: Capsule())
    }
}

/// Dismissible message banner.
struct BannerView: View {
    let text: String
    let onDismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.circle.fill").foregroundStyle(Theme.warning)
            Text(text).font(.subheadline).frame(maxWidth: .infinity, alignment: .leading)
            Button(action: onDismiss) { Image(systemName: "xmark").font(.caption.bold()) }
                .buttonStyle(.plain)
                .accessibilityLabel("Dismiss")
        }
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .padding(.horizontal)
        .transition(.move(edge: .top).combined(with: .opacity))
    }
}

/// Round command button used on the dashboard.
struct ActionButton: View {
    let title: String
    let systemImage: String
    var active = false
    var busy = false
    var disabled = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                ZStack {
                    Circle()
                        .fill(active ? Theme.accent : Color.secondary.opacity(0.14))
                        .frame(width: 52, height: 52)
                    if busy {
                        ProgressView().tint(active ? .white : .primary)
                    } else {
                        Image(systemName: systemImage)
                            .font(.system(size: 20, weight: .semibold))
                            .foregroundStyle(active ? Color.white : Color.primary)
                    }
                }
                Text(title).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.plain)
        .disabled(disabled || busy)
        .opacity(disabled ? 0.4 : 1)
        .accessibilityLabel(title)
    }
}

extension View {
    /// Shows the app-wide banner above the content.
    func appBanner(_ text: Binding<String?>) -> some View {
        overlay(alignment: .top) {
            if let t = text.wrappedValue {
                BannerView(text: t) { withAnimation { text.wrappedValue = nil } }
                    .padding(.top, 4)
                    .task(id: t) {
                        try? await Task.sleep(nanoseconds: 6_000_000_000)
                        withAnimation { if text.wrappedValue == t { text.wrappedValue = nil } }
                    }
            }
        }
        .animation(.spring(duration: 0.3), value: text.wrappedValue)
    }
}
