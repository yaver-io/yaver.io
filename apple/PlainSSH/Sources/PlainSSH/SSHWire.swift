import Foundation
import Crypto
import NIOCore
import NIOSSH
import NIOTransportServices

public struct SSHFailure: LocalizedError { public let message:String; public var errorDescription:String?{message}; public init(_ message:String){self.message=message} }
private final class PasswordAuth: NIOSSHClientUserAuthenticationDelegate {
 let user:String;let password:String;let key:NIOSSHPrivateKey?;var offered=false
 init(_ user:String,_ password:String,_ key:NIOSSHPrivateKey?){self.user=user;self.password=password;self.key=key}
 func nextAuthenticationType(availableMethods:NIOSSHAvailableUserAuthenticationMethods,nextChallengePromise:EventLoopPromise<NIOSSHUserAuthenticationOffer?>){
  guard !offered else {nextChallengePromise.succeed(nil);return};offered=true
  if let key {nextChallengePromise.succeed(.init(username:user,serviceName:"",offer:.privateKey(.init(privateKey:key))));return}
  if password.isEmpty { nextChallengePromise.succeed(.init(username:user,serviceName:"",offer:.none));return }
  guard availableMethods.contains(.password) else {nextChallengePromise.fail(SSHFailure("This SSH server requires a key. Use the phone's SSH-key connection or enable an SSH login method supported here."));return}
  nextChallengePromise.succeed(.init(username:user,serviceName:"",offer:.password(.init(password:password))))
 }
}
private final class HostObservation {
 let promise:EventLoopPromise<String>;private let lock=NSLock();private var completed=false
 init(_ promise:EventLoopPromise<String>){self.promise=promise}
 func complete(_ value:Result<String,Error>){lock.lock();guard !completed else{lock.unlock();return};completed=true;lock.unlock();promise.completeWith(value)}
}
private final class HostPin: NIOSSHClientServerAuthenticationDelegate {
 let expected:String?; let observed:HostObservation?
 init(_ expected:String?,_ observed:HostObservation?=nil){self.expected=expected;self.observed=observed}
 func validateHostKey(hostKey:NIOSSHPublicKey,validationCompletePromise:EventLoopPromise<Void>){
  let fields=String(openSSHPublicKey:hostKey).split(separator:" ")
  guard fields.count>1,let bytes=Data(base64Encoded:String(fields[1])) else {validationCompletePromise.fail(SSHFailure("Cannot read the SSH host key."));return}
  let fingerprint="SHA256:"+Data(SHA256.hash(data:bytes)).base64EncodedString().replacingOccurrences(of:"=",with:"")
  observed?.complete(.success(fingerprint))
  guard let expected, expected==fingerprint else {validationCompletePromise.fail(SSHFailure(expected == nil ? "Host key observed without authentication." : "SSH host key changed. Verify it on the machine before replacing this saved host."));return}
  validationCompletePromise.succeed(())
 }
}
private final class ExecCapture: ChannelInboundHandler {
 typealias InboundIn=SSHChannelData
 let command:String;let result:EventLoopPromise<String>;let output:((String)->Void)?
 var bytes=Data();var status:Int?;var finished=false
 init(_ command:String,_ result:EventLoopPromise<String>,_ output:((String)->Void)?){self.command=command;self.result=result;self.output=output}
 func channelActive(context:ChannelHandlerContext){context.triggerUserOutboundEvent(SSHChannelRequestEvent.ExecRequest(command:command,wantReply:true),promise:nil)}
 func channelRead(context:ChannelHandlerContext,data:NIOAny){
  let value=unwrapInboundIn(data);guard case .byteBuffer(let b)=value.data else{return}
  let chunk=Data(b.readableBytesView);output?(String(decoding:chunk,as:UTF8.self));bytes.append(chunk)
  if bytes.count>1024*1024{finish(.failure(SSHFailure("SSH output exceeded 1 MB.")));context.close(promise:nil)}
 }
 func userInboundEventTriggered(context:ChannelHandlerContext,event:Any){if let e=event as? SSHChannelRequestEvent.ExitStatus{status=e.exitStatus};context.fireUserInboundEventTriggered(event)}
 func channelInactive(context:ChannelHandlerContext){if status==0{finish(.success(String(decoding:bytes,as:UTF8.self)))}else{finish(.failure(SSHFailure(String(decoding:bytes,as:UTF8.self).trimmingCharacters(in:.whitespacesAndNewlines).isEmpty ? "SSH command did not complete. Check the connection and retry." : String(decoding:bytes,as:UTF8.self))))}}
 func errorCaught(context:ChannelHandlerContext,error:Error){finish(.failure(error));context.close(promise:nil)}
 func finish(_ value:Result<String,Error>){guard !finished else{return};finished=true;result.completeWith(value)}
}

/// All commands stay inside an independently authenticated, host-pinned SSH
/// channel. This module never reads a Yaver token, registry, or relay address.
public final class SSHWire {
 private static let group=NIOTSEventLoopGroup(loopCount:1)
 private var channel:Channel?
 public init(){}
 private static func dial(host:String,port:Int,user:String,password:String,privateKey:String="",pin:HostPin) async throws -> Channel {
  guard !host.isEmpty,(1...65535).contains(port),!user.isEmpty else{throw SSHFailure("Enter an SSH host, port and username.")}
  let key=privateKey.isEmpty ? nil : try OpenSSHKey.parse(privateKey)
  return try await NIOTSConnectionBootstrap(group:group).connectTimeout(.seconds(12)).channelInitializer{channel in
   channel.pipeline.addHandler(NIOSSHHandler(role:.client(.init(userAuthDelegate:PasswordAuth(user,password,key),serverAuthDelegate:pin)),allocator:channel.allocator,inboundChildChannelInitializer:nil))
  }.connect(host:host,port:port).get()
 }
 public static func probe(host:String,port:Int,user:String) async throws -> String {
  let loop=group.next();let result=loop.makePromise(of:String.self);let observation=HostObservation(result)
  let connection=try await dial(host:host,port:port,user:user,password:"",pin:HostPin(nil,observation))
  let timeout=loop.scheduleTask(in:.seconds(15)){observation.complete(.failure(SSHFailure("SSH host-key check timed out. Check Tailscale and Remote Login.")))}
  defer{timeout.cancel();connection.close(promise:nil)}
  return try await result.futureResult.get()
 }
 public func connect(host:String,port:Int,user:String,password:String,privateKey:String="",fingerprint:String) async throws {
  guard !fingerprint.isEmpty else{throw SSHFailure("Verify the host fingerprint first.")}
  close();channel=try await Self.dial(host:host,port:port,user:user,password:password,privateKey:privateKey,pin:HostPin(fingerprint))
  // This real operation proves SSH authorization; a TCP connection does not.
  _=try await execute("printf yaver-ssh-ready")
 }
 public func execute(_ command:String,seconds:Int64=12,output:((String)->Void)?=nil) async throws -> String {
  guard let channel,channel.isActive else{throw SSHFailure("SSH disconnected. Reconnect to this host.")}
  let result=channel.eventLoop.makePromise(of:String.self)
  let child=channel.eventLoop.makePromise(of:Channel.self)
  channel.pipeline.handler(type:NIOSSHHandler.self).whenComplete{value in
   switch value {case .failure(let error):child.fail(error)
   case .success(let ssh):ssh.createChannel(child){c,type in
    guard type == .session else{return c.eventLoop.makeFailedFuture(SSHFailure("SSH session channel refused."))}
    return c.pipeline.addHandler(ExecCapture(command,result,output))
   }}
  }
  child.futureResult.whenFailure{result.fail($0)}
  let timeout=channel.eventLoop.scheduleTask(in:.seconds(seconds)) {child.futureResult.whenSuccess{$0.close(promise:nil)};channel.close(promise:nil)}
  defer{timeout.cancel()}
  return try await result.futureResult.get()
 }
 public func close(){channel?.close(promise:nil);channel=nil}
 deinit{close()}
}
