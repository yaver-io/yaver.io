import Foundation
import SwiftUI
import Security

public struct SSHHost: Codable, Identifiable {public var id=UUID().uuidString;public var host:String;public var port:Int;public var user:String;public var fingerprint:String}
public struct SSHPane:Identifiable,Equatable {
 public let id:String;public let session:String;public let name:String;public let window:String;public let command:String;public let identity:String
 public func matches(_ other:SSHPane)->Bool{id==other.id && session==other.session && identity==other.identity}
 public var label:String{"\(name) · \(window) · \(id) · \(command)"}
}
public enum PaneCommands {
 public static let path="PATH=\"$PATH:/opt/homebrew/bin:/usr/local/bin\"; export PATH; "
 public static let list=path+"tmux -u list-panes -a -F '#{pane_id}\t#{session_id}\t#{session_name}\t#{window_index}\t#{pane_current_command}\t#{pid}:#{pane_pid}:#{session_created}'"
 public static func quote(_ text:String)->String{"'"+text.replacingOccurrences(of:"'",with:"'\\''")+"'"}
 public static func valid(_ pane:String)->Bool{pane.range(of:"^%[0-9]+$",options:.regularExpression) != nil}
 public static func parse(_ text:String)->[SSHPane]{text.split(separator:"\n").compactMap{line in
  let f=line.split(separator:"\t",omittingEmptySubsequences:false).map(String.init)
  guard f.count==6,valid(f[0]),f[1].range(of:"^\\$[0-9]+$",options:.regularExpression) != nil else{return nil}
  return SSHPane(id:f[0],session:f[1],name:f[2],window:f[3],command:f[4],identity:f[5])
 }}
}
@MainActor public final class PaneModel:ObservableObject {
 @Published public var hosts:[SSHHost]=[]
 @Published public var panes:[SSHPane]=[]
 @Published public var pane:SSHPane?
 @Published public var output=""
 @Published public var enrollment=""
 @Published public var status=""
 @Published public var error=""
 @Published public var busy=false
 @Published public var connected=false
 @Published public var lastInput=""
 private var wire=SSHWire();private var poll:Task<Void,Never>?;private var generation=0
 private let storage="yaver.plainSSH.native.hosts.v1"
 public init(){if let data=UserDefaults.standard.data(forKey:storage){hosts=(try? JSONDecoder().decode([SSHHost].self,from:data)) ?? []}}
 private func credentials(_ id:String,_ password:String?=nil)throws->String{
  let query:[String:Any]=[kSecClass as String:kSecClassGenericPassword,kSecAttrService as String:"io.yaver.plainSSH",kSecAttrAccount as String:id]
  if let password {SecItemDelete(query as CFDictionary);var item=query;item[kSecValueData as String]=Data(password.utf8);item[kSecAttrAccessible as String]=kSecAttrAccessibleWhenUnlockedThisDeviceOnly;guard SecItemAdd(item as CFDictionary,nil)==errSecSuccess else{throw SSHFailure("Could not save SSH credentials in this device's Keychain.")};return password}
  var read=query;read[kSecReturnData as String]=true;var result:CFTypeRef?;guard SecItemCopyMatching(read as CFDictionary,&result)==errSecSuccess,let data=result as? Data else{throw SSHFailure("Saved SSH password is unavailable. Forget and add this host again.")};return String(decoding:data,as:UTF8.self)
 }
 public func remember(_ host:SSHHost,password:String,privateKey:String="")async{do{let secret=try JSONEncoder().encode(["password":password,"privateKey":privateKey]);_=try credentials(host.id,String(decoding:secret,as:UTF8.self));hosts.removeAll{$0.id==host.id};hosts.append(host);UserDefaults.standard.set(try JSONEncoder().encode(hosts),forKey:storage);await connect(host)}catch{self.error=error.localizedDescription}}
 public func forget(_ host:SSHHost){SecItemDelete([kSecClass as String:kSecClassGenericPassword,kSecAttrService as String:"io.yaver.plainSSH",kSecAttrAccount as String:host.id] as CFDictionary);hosts.removeAll{$0.id==host.id};UserDefaults.standard.set(try? JSONEncoder().encode(hosts),forKey:storage)}
 public func connect(_ host:SSHHost)async{
  detach();busy=true;error="";status="Connecting over SSH…";let attempt=generation
  do{let stored=try credentials(host.id);let secret=(try? JSONDecoder().decode([String:String].self,from:Data(stored.utf8))) ?? ["password":stored];try await wire.connect(host:host.host,port:host.port,user:host.user,password:secret["password"] ?? "",privateKey:secret["privateKey"] ?? "",fingerprint:host.fingerprint)
   guard attempt==generation else{return};let found=try await list();guard attempt==generation else{return};panes=found;connected=true;status=panes.isEmpty ? "No tmux panes. Start tmux on this SSH account, then reconnect." : "Choose your existing pane"
  }catch{if attempt==generation{self.error=error.localizedDescription;wire.close()}}
  if attempt==generation{busy=false}
 }
 private func list()async throws->[SSHPane]{PaneCommands.parse(try await wire.execute(PaneCommands.list))}
 public func attach(_ selected:SSHPane)async{
  error="";busy=true;let attempt=generation
  do{guard try await list().contains(where:{$0.matches(selected)}) else{throw SSHFailure("This pane exited or changed. Refresh the host before sending input.")};guard attempt==generation else{return};pane=selected;output="";lastInput="";status=selected.label
   poll?.cancel();let attempt=generation
   poll=Task{while !Task.isCancelled && attempt==generation{do{
    // Exact-pane snapshots preserve the remote layout and desktop selection.
    guard try await list().contains(where:{$0.matches(selected)}) else{throw SSHFailure("Selected pane exited. Reconnect to select another.")}
    output=try await wire.execute(PaneCommands.path+"tmux -u capture-pane -p -t "+selected.id)
    try await Task.sleep(nanoseconds:800_000_000)
   }catch{if !Task.isCancelled{self.error=error.localizedDescription;connected=false};break}}}
  }catch{self.error=error.localizedDescription};busy=false
 }
 public func send(_ text:String,submit:Bool=true)async{
  guard connected,let pane else{error="Reconnect before sending input.";return}
  guard !text.isEmpty,text.utf8.count<=32768 else{error="Input must contain 1–32768 bytes.";return}
  busy=true;let attempt=generation;defer{busy=false}
  do{guard try await list().contains(where:{$0.matches(pane)}) else{throw SSHFailure("Selected pane exited. Input was not sent.")}
   guard attempt==generation else{throw SSHFailure("Connection changed. Input was not sent.")}
   let command:String
   if submit{let name="yaver-phone-"+UUID().uuidString;command="tmux -u set-buffer -b \(name) -- \(PaneCommands.quote(text)); tmux -u paste-buffer -d -p -b \(name) -t \(pane.id) && tmux -u send-keys -t \(pane.id) Enter"}
   else{command="tmux -u send-keys -t \(pane.id) -H "+text.utf8.map{String(format:"%02x",$0)}.joined(separator:" ")}
   _=try await wire.execute(PaneCommands.path+command);if submit{lastInput=text}
  }catch{self.error=error.localizedDescription+" Inspect the pane before retrying; input is never replayed."}
 }
 public func enroll(install:Bool=false)async{
  guard connected else{return};busy=true;enrollment="";error="";let attempt=generation
  let command=install ? "command -v npm >/dev/null || { echo 'Install Node.js/npm on this host first.'; exit 127; }; npm install -g yaver-cli" : "command -v yaver >/dev/null || { echo 'Yaver is not installed. Use Install Yaver.'; exit 127; }; YAVER_NO_QR=1 yaver auth --headless"
  do{_=try await wire.execute(PaneCommands.path+command,seconds:900){[weak self] chunk in Task{@MainActor in guard let self, self.generation==attempt else{return};self.enrollment=String((self.enrollment+chunk).suffix(16000))}}}
  catch{self.error=error.localizedDescription};busy=false
 }
 public func detach(){generation+=1;poll?.cancel();poll=nil;wire.close();wire=SSHWire();connected=false;pane=nil;panes=[];busy=false;status="Detached · remote panes keep running"}
}
