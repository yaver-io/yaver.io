import SwiftUI

/// The same account-independent native screen is embedded by TV, Watch and
/// Vision. Closing it detaches only the viewer; Yaver auth never gates it.
public struct PlainSSHView:View {
 @StateObject private var model=PaneModel()
 @Environment(\.dismiss) private var dismiss
 @Environment(\.scenePhase) private var phase
 @State private var host="";@State private var port="22";@State private var user="";@State private var password=""
 @State private var privateKey=""
 @State private var candidate:SSHHost?;@State private var probing=false;@State private var chat=false;@State private var draft="";@State private var account=false
 public init(){}
 public var body:some View {
  NavigationStack {
   ScrollView {
    VStack(alignment:.leading,spacing:12){
     if !model.status.isEmpty{Text(model.status).font(.caption).foregroundStyle(.secondary)}
     if !model.error.isEmpty{Text(model.error).foregroundStyle(.red).accessibilityIdentifier("ssh-error")}
     if model.pane != nil {
      #if os(watchOS)
      Button(chat ? "Pane chat · switch to Raw" : "Raw · switch to Pane chat"){chat.toggle()}
      #else
      Picker("Pane view",selection:$chat){Text("Raw").tag(false);Text("Pane chat").tag(true)}.pickerStyle(.segmented)
      #endif
      if chat && !model.lastInput.isEmpty{Text(model.lastInput).padding(10).background(.quaternary,in:RoundedRectangle(cornerRadius:10)).frame(maxWidth:.infinity,alignment:.trailing)}
      Text("Live tmux screen · refreshes every second").font(.caption2).foregroundStyle(.secondary)
      if chat{Text("Live pane").font(.caption).foregroundStyle(.secondary)}
      ScrollView(chat ? .vertical : [.horizontal,.vertical]){Text(model.output.isEmpty ? "Reading pane…" : model.output).font(.system(size:13,design:.monospaced)).frame(maxWidth:.infinity,alignment:.leading)}.frame(minHeight:140)
      TextField("Message to selected pane",text:$draft).accessibilityIdentifier("ssh-input")
      Button("Send"){let text=draft;Task{await model.send(text);if model.error.isEmpty && draft==text{draft=""}}}.disabled(model.busy || !model.connected || draft.isEmpty)
      HStack{Button("Enter"){Task{await model.send("\r",submit:false)}};Button("Esc"){Task{await model.send("\u{1b}",submit:false)}};Button("Ctrl-C"){Task{await model.send("\u{3}",submit:false)}}}.disabled(!model.connected || model.busy)
     } else if model.connected {
      ForEach(model.panes){pane in Button(pane.label){Task{await model.attach(pane)}}.disabled(model.busy)}
     } else {
      Text("SSH over your network or Tailscale. No Yaver account required.").font(.caption).foregroundStyle(.secondary)
      ForEach(model.hosts){saved in HStack{Button("\(saved.user)@\(saved.host)"){Task{await model.connect(saved)}};Button("Forget"){model.forget(saved)}}.disabled(model.busy)}
      TextField("Hostname or Tailscale address",text:$host)
      TextField("SSH port",text:$port)
      TextField("SSH username",text:$user)
      SecureField("SSH password",text:$password)
      SecureField("OpenSSH Ed25519 key (optional)",text:$privateKey)
      Text("Leave the password empty only when Tailscale SSH policy permits this user. Ordinary SSH still needs its own credentials.").font(.caption2).foregroundStyle(.secondary)
      if let candidate {
       Text("Verify this host key on the remote machine:").font(.caption)
       Text(candidate.fingerprint).font(.system(size:11,design:.monospaced))
       Button("Trust host and connect"){Task{await model.remember(candidate,password:password,privateKey:privateKey);self.candidate=nil;password="";privateKey=""}}
      } else {Button(probing ? "Checking host key…" : "Check host key"){Task{await probe()}}.disabled(probing || model.busy || host.isEmpty || user.isEmpty)}
     }
     if model.connected {
      Button(account ? "Hide Yaver setup" : "Connect remote to Yaver"){account.toggle()}
      if account {
       Text("Optional setup in a separate SSH channel. Your pane stays running.").font(.caption)
       Button("Sign this remote into Yaver"){Task{await model.enroll()}}.disabled(model.busy)
       if model.enrollment.contains("Yaver is not installed"){Button("Install Yaver on remote"){Task{await model.enroll(install:true)}}.disabled(model.busy)}
       RemoteApprovalView(output:model.enrollment)
       if !model.enrollment.isEmpty{Text(model.enrollment).font(.system(size:11,design:.monospaced))}
      }
      Button("Detach"){model.detach()}
     }
    }.padding()
   }
   .navigationTitle("Plain SSH")
   .toolbar{ToolbarItem(placement:.cancellationAction){Button("Back"){model.detach();dismiss()}}}
  }
  .onDisappear{model.detach()}
  .onChange(of:phase){_,phase in if phase == .background{model.detach()}}
  .onChange(of:host){_,_ in candidate=nil}.onChange(of:port){_,_ in candidate=nil}.onChange(of:user){_,_ in candidate=nil}
 }
 private func probe()async{probing=true;model.error="";defer{probing=false};do{guard let number=Int(port),(1...65535).contains(number) else{throw SSHFailure("Enter an SSH port between 1 and 65535.")};let fingerprint=try await SSHWire.probe(host:host,port:number,user:user);candidate=SSHHost(host:host,port:number,user:user,fingerprint:fingerprint)}catch{model.error=error.localizedDescription}}
}
