import XCTest
@testable import PlainSSH
final class PlainSSHTests:XCTestCase {
 func testPaneValidation(){XCTAssertTrue(PaneCommands.valid("%10"));for value in ["%1; kill-server","%1\nsend-keys","work",""]{XCTAssertFalse(PaneCommands.valid(value))};XCTAssertEqual(PaneCommands.quote("a'b"),"'a'\\''b'")}
 func testIdentitySurvivesCommandChange(){let a=PaneCommands.parse("%1\t$0\twork\t0\tsh\t1:2:3")[0];let b=PaneCommands.parse("%1\t$0\twork\t0\trunner\t1:2:3")[0];XCTAssertTrue(a.matches(b));XCTAssertFalse(a.matches(PaneCommands.parse("%1\t$0\twork\t0\tsh\t1:9:3")[0]))}
 func testOpenSSHKeyParser()throws{
  let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString);try FileManager.default.createDirectory(at:directory,withIntermediateDirectories:true);defer{try? FileManager.default.removeItem(at:directory)}
  let path=directory.appendingPathComponent("key");let process=Process();process.executableURL=URL(fileURLWithPath:"/usr/bin/ssh-keygen");process.arguments=["-q","-t","ed25519","-N","","-f",path.path];try process.run();process.waitUntilExit();XCTAssertEqual(process.terminationStatus,0)
  let pem=try String(contentsOf:path,encoding:.utf8);XCTAssertNoThrow(try OpenSSHKey.parse(pem));XCTAssertThrowsError(try OpenSSHKey.parse("truncated"));XCTAssertThrowsError(try OpenSSHKey.parse(String(pem.prefix(80))))
 }
 func testRealSSH()async throws{
  guard let path=ProcessInfo.processInfo.environment["PLAIN_SSH_BROWSER_FIXTURE"] else{throw XCTSkip("real SSH fixture not requested")}
  let json=try JSONSerialization.jsonObject(with:Data(contentsOf:URL(fileURLWithPath:path))) as! [String:Any]
  let host=json["host"] as! String,port=json["port"] as! Int,user=json["user"] as! String,password=json["password"] as! String,pin=json["fingerprint"] as! String
  let observed=try await SSHWire.probe(host:host,port:port,user:user);XCTAssertEqual(observed,pin)
  let client=SSHWire();defer{client.close()}
  do{try await client.connect(host:host,port:port,user:user,password:password,fingerprint:"SHA256:wrong");XCTFail("changed host key accepted")}catch{}
  try await client.connect(host:host,port:port,user:user,password:password,fingerprint:pin)
  let panes=PaneCommands.parse(try await client.execute(PaneCommands.list));XCTAssertFalse(panes.isEmpty)
  _=try await client.execute(PaneCommands.path+"tmux send-keys -t "+panes[0].id+" -H 53 57 49 46 54 0d")
  try await Task.sleep(nanoseconds:100_000_000)
  let output=try await client.execute(PaneCommands.path+"tmux capture-pane -p -t "+panes[0].id);XCTAssertTrue(output.contains("SWIFT"))
 }
}
