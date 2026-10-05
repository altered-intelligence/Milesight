import SwiftUI

struct VehiclesListView: View {
    var body: some View {
        NavigationStack {
            List {
                Text("No vehicles yet.")
                    .foregroundStyle(.secondary)
            }
            .navigationTitle("Milesight")
        }
    }
}
