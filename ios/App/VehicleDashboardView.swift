import SwiftUI
import CoreKit

/// Placeholder dashboard showing live battery state once wired to the store.
struct VehicleDashboardView: View {
    let vehicle: Vehicle

    var body: some View {
        VStack(spacing: 24) {
            Text(vehicle.displayName ?? vehicle.id)
                .font(.title.bold())
            Text("Live telemetry coming soon.")
                .foregroundStyle(.secondary)
        }
        .navigationTitle(vehicle.displayName ?? vehicle.id)
    }
}
