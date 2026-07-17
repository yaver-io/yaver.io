// GuestAccessView.swift — share management on the wrist: see who shared with
// me, accept/leave as a guest, and remove guests as the host with no typing.
//
// Why this is coherent on a surface with NO device list: guest access is
// anchored to a HOST (a person), not to a box. `/guests/hosts` is Convex-direct
// and keyed only by the session token, so it returns a short list of people
// without the watch knowing a single deviceId. The watch's "one box by address"
// model is simply not involved — same reasoning as Settings' "Update agent",
// which also reaches the ACCOUNT rather than any box.
//
// Reachable ONLY behind the standalone opt-in, because that is the only mode in
// which the watch holds a token at all (docs/yaver-smartwatch-voice-terminal.md
// §8 "standalone token custody"). In the default phone-paired mode the watch
// holds nothing and the phone is the brain-of-record — there is no token to
// authenticate these calls with, so the entry point is hidden rather than shown
// broken.
//
// Host-side invite stays absent: it needs an email typed in, which is hostile on
// a wrist. Host-side revoke is viable because the list already names the guest.

import SwiftUI

struct GuestAccessView: View {
    @EnvironmentObject var store: WatchStore

    /// Load state. `.loaded` carries the list so an empty list is a real answer
    /// ("nobody shared anything") rather than an indistinguishable blank.
    private enum LoadState: Equatable {
        case loading
        case loaded(GuestOverview)
        case failed(String)
    }

    private struct GuestOverview: Equatable {
        let hosts: GuestHosts
        let guests: HostGuests

        var isEmpty: Bool { hosts.isEmpty && acceptedGuests.isEmpty }
        var acceptedGuests: [HostGuest] { guests.guests.filter(\.isAccepted) }
    }

    @State private var state: LoadState = .loading
    /// The host the user asked to leave; non-nil drives the confirm sheet.
    @State private var leaving: GuestActiveHost?
    /// The guest the user asked to revoke; non-nil drives the confirm sheet.
    @State private var revoking: HostGuest?
    /// A row-level action in flight (accept or leave), keyed by row id, so the
    /// whole list doesn't flip to a spinner for one tap.
    @State private var busyID: String?
    @State private var actionError: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                switch state {
                case .loading:
                    HStack(spacing: 6) {
                        ProgressView()
                        Text("Loading…").font(.footnote)
                    }
                case .failed(let message):
                    Text(message).font(.footnote).foregroundStyle(.orange)
                    Button("Try again") { Task { await load() } }.font(.footnote)
                case .loaded(let overview):
                    if overview.isEmpty {
                        Text("No shared access right now.")
                            .font(.footnote).foregroundStyle(.secondary)
                    } else {
                        if !overview.hosts.pending.isEmpty {
                            sectionTitle("Invitations")
                            ForEach(overview.hosts.pending) { pendingRow($0) }
                        }
                        if !overview.hosts.active.isEmpty {
                            if !overview.hosts.pending.isEmpty { Divider() }
                            sectionTitle("Shared with you")
                            ForEach(overview.hosts.active) { activeRow($0) }
                        }
                        if !overview.acceptedGuests.isEmpty {
                            if !overview.hosts.pending.isEmpty || !overview.hosts.active.isEmpty { Divider() }
                            sectionTitle("Shared by you")
                            ForEach(overview.acceptedGuests) { guestRow($0) }
                        }
                    }
                }

                if let actionError {
                    Text(actionError).font(.caption2).foregroundStyle(.orange)
                }
            }
            .padding(.horizontal, 6)
        }
        .navigationTitle("Access")
        .task { await load() }
        // Leaving is destructive and easy to misfire on a wrist, so it goes
        // through the same explicit confirm gate every risky verb uses.
        .sheet(item: $leaving) { host in
            ConfirmView(prompt: confirmPrompt(for: host)) { reply in
                guard reply == .confirm else { return }
                Task { await leave(host) }
            }
        }
        .sheet(item: $revoking) { guest in
            ConfirmView(prompt: revokePrompt(for: guest)) { reply in
                guard reply == .confirm else { return }
                Task { await revoke(guest) }
            }
        }
    }

    @ViewBuilder private func sectionTitle(_ text: String) -> some View {
        Text(text.uppercased())
            .font(.system(size: 11, weight: .bold)).tracking(1)
            .foregroundStyle(.secondary)
    }

    /// A pending invite: accept it with the code already on the row, so nothing
    /// has to be typed on the wrist.
    @ViewBuilder private func pendingRow(_ invite: GuestPendingInvite) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(invite.hostName).font(.footnote).fontWeight(.semibold)
            Text(invite.hostEmail).font(.caption2).foregroundStyle(.secondary)
            if busyID == invite.id {
                HStack(spacing: 6) {
                    ProgressView()
                    Text("Accepting…").font(.caption2)
                }
            } else {
                Button("Accept") { Task { await accept(invite) } }
                    .font(.footnote)
                    .disabled(busyID != nil)
            }
        }
        .padding(.vertical, 2)
    }

    /// An active host. The device count is shown because it is exactly what
    /// "remove my access" is about to cover.
    @ViewBuilder private func activeRow(_ host: GuestActiveHost) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(host.hostName).font(.footnote).fontWeight(.semibold)
            Text(host.hostEmail).font(.caption2).foregroundStyle(.secondary)
            if host.deviceCount > 0 {
                Text(host.deviceCount == 1 ? "1 machine" : "\(host.deviceCount) machines")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            if busyID == host.id {
                HStack(spacing: 6) {
                    ProgressView()
                    Text("Removing…").font(.caption2)
                }
            } else {
                Button("Remove my access", role: .destructive) { leaving = host }
                    .font(.footnote)
                    .disabled(busyID != nil)
            }
        }
        .padding(.vertical, 2)
    }

    /// A guest on the HOST side. Name + email both stay visible so a wrist tap
    /// is less likely to remove the wrong person.
    @ViewBuilder private func guestRow(_ guest: HostGuest) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(guest.displayName).font(.footnote).fontWeight(.semibold)
            Text(guest.detail).font(.caption2).foregroundStyle(.secondary)
            if busyID == guest.id {
                HStack(spacing: 6) {
                    ProgressView()
                    Text("Removing…").font(.caption2)
                }
            } else {
                Button("Remove access", role: .destructive) { revoking = guest }
                    .font(.footnote)
                    .disabled(busyID != nil)
            }
        }
        .padding(.vertical, 2)
    }

    /// Say what actually happens: it covers EVERY machine this host shared (not
    /// just one), and it is reversible by re-invitation — both facts the user
    /// needs before a destructive tap they can't inspect on a wrist.
    private func confirmPrompt(for host: GuestActiveHost) -> String {
        let scope = host.deviceCount > 0
            ? (host.deviceCount == 1 ? "the 1 machine" : "all \(host.deviceCount) machines")
            : "every machine"
        return "Remove your access to \(scope) \(host.hostName) shared? They can invite you again."
    }

    private func revokePrompt(for guest: HostGuest) -> String {
        "Remove \(guest.displayName)'s access to every machine you shared? They'd need a fresh invitation to come back."
    }

    private func load() async {
        state = .loading
        actionError = nil
        do {
            async let hosts = GuestAccess.hosts(token: store.token)
            async let guests = GuestAccess.guests(token: store.token)
            state = .loaded(GuestOverview(hosts: try await hosts, guests: try await guests))
        } catch {
            state = .failed(friendly(error))
        }
    }

    private func accept(_ invite: GuestPendingInvite) async {
        busyID = invite.id
        actionError = nil
        defer { busyID = nil }
        do {
            try await GuestAccess.acceptCode(invite.inviteCode, token: store.token)
            await load()   // the invite moves pending → active server-side
        } catch {
            actionError = friendly(error)
        }
    }

    private func leave(_ host: GuestActiveHost) async {
        busyID = host.id
        actionError = nil
        defer { busyID = nil }
        do {
            // By email, NOT the reported hostUserId — see GuestAccess.leave.
            try await GuestAccess.leave(hostEmail: host.hostEmail, token: store.token)
            await load()
        } catch {
            actionError = friendly(error)
        }
    }

    private func revoke(_ guest: HostGuest) async {
        busyID = guest.id
        actionError = nil
        defer { busyID = nil }
        do {
            try await GuestAccess.revoke(email: guest.email, userId: guest.userId, token: store.token)
            await load()
        } catch {
            actionError = friendly(error)
        }
    }

    private func friendly(_ error: Error) -> String {
        if let a = error as? AgentError { return a.message }
        return "Couldn't reach Yaver."
    }
}
