import SwiftUI

struct LoginView: View {
    @State private var username: String = Auth.validUsername
    @State private var password: String = Auth.validPassword
    @State private var error: String? = nil
    @State private var signedInUser: String? = nil

    var body: some View {
        NavigationStack {
            Form {
                Section(header: Text("Sign in to Yaver Fixture")) {
                    Text("Hardcoded creds: admin / admin")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    TextField("Username", text: $username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled(true)
                    SecureField("Password", text: $password)
                }
                Section {
                    Button("Sign in") {
                        if Auth.authenticate(username: username, password: password) {
                            signedInUser = username
                            error = nil
                        } else {
                            error = "Invalid credentials. Use admin / admin."
                        }
                    }
                    if let error = error {
                        Text(error).foregroundColor(.red).font(.caption)
                    }
                }
            }
            .navigationTitle("Yaver Fixture")
            .navigationDestination(isPresented: Binding(
                get: { signedInUser != nil },
                set: { if !$0 { signedInUser = nil } }
            )) {
                DashboardView(username: signedInUser ?? "guest")
            }
        }
    }
}

#Preview {
    LoginView()
}
