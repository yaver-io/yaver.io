import Foundation
import Crypto
import NIOSSH

/// Strict unencrypted Ed25519 OpenSSH key reader. Encrypted/RSA keys remain
/// supported by the phone/desktop Go lane; this native lane names the limit.
enum OpenSSHKey {
 static func parse(_ pem:String)throws->NIOSSHPrivateKey {
  let text=pem.replacingOccurrences(of:"-----BEGIN OPENSSH PRIVATE KEY-----",with:"").replacingOccurrences(of:"-----END OPENSSH PRIVATE KEY-----",with:"").filter{!$0.isWhitespace}
  guard let data=Data(base64Encoded:text),data.starts(with:Data("openssh-key-v1\0".utf8)) else{throw SSHFailure("Use an OpenSSH Ed25519 key. Other key formats are supported in the phone or desktop SSH client.")}
  var reader=Reader(bytes:Array(data),offset:15)
  guard try reader.string()==Data("none".utf8),try reader.string()==Data("none".utf8) else{throw SSHFailure("This native surface supports unencrypted Ed25519 keys. Use the phone or desktop SSH client for encrypted keys.")}
  _=try reader.string();guard try reader.uint32()==1 else{throw SSHFailure("Expected one SSH key.")};_=try reader.string();var privateReader=Reader(bytes:Array(try reader.string()))
  let check=try privateReader.uint32();guard try privateReader.uint32()==check,try privateReader.string()==Data("ssh-ed25519".utf8) else{throw SSHFailure("Invalid Ed25519 OpenSSH private key.")}
  let publicKey=try privateReader.string();let secret=try privateReader.string();guard secret.count==64,publicKey.count==32 else{throw SSHFailure("Invalid Ed25519 key length.")}
  let key=try Curve25519.Signing.PrivateKey(rawRepresentation:secret.prefix(32));guard key.publicKey.rawRepresentation==publicKey,secret.suffix(32)==publicKey else{throw SSHFailure("SSH private/public key mismatch.")}
  return NIOSSHPrivateKey(ed25519Key:key)
 }
 private struct Reader {let bytes:[UInt8];var offset=0
  mutating func uint32()throws->UInt32{guard offset+4<=bytes.count else{throw SSHFailure("Truncated SSH key.")};let n=bytes[offset..<offset+4].reduce(UInt32(0)){($0<<8)|UInt32($1)};offset+=4;return n}
  mutating func string()throws->Data{let count=Int(try uint32());guard count<=bytes.count-offset else{throw SSHFailure("Truncated SSH key.")};defer{offset+=count};return Data(bytes[offset..<offset+count])}
 }
}
