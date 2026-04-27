import SwiftUI

struct DashboardView: View {
    let username: String

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "checkmark.seal.fill")
                .resizable()
                .scaledToFit()
                .frame(width: 64, height: 64)
                .foregroundColor(.green)
            Text("Hello, \(username)")
                .font(.largeTitle)
            Text("You are signed in to the Yaver iOS fixture.")
                .font(.body)
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
        }
        .padding()
        .navigationTitle("Dashboard")
    }
}

#Preview {
    DashboardView(username: "admin")
}
