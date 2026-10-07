// swift-tools-version:5.9
import PackageDescription
let package = Package(name:"PlainSSH", platforms:[.macOS(.v14),.iOS(.v17),.tvOS(.v17),.watchOS(.v10),.visionOS("2.0")], products:[.library(name:"PlainSSH",targets:["PlainSSH"])], dependencies:[
 .package(url:"https://github.com/apple/swift-nio-ssh.git",exact:"0.15.0"),
 .package(url:"https://github.com/apple/swift-nio-transport-services.git",from:"1.24.0"),
 .package(url:"https://github.com/apple/swift-crypto.git",from:"3.12.3")
],targets:[.target(name:"PlainSSH",dependencies:[.product(name:"NIOSSH",package:"swift-nio-ssh"),.product(name:"NIOTransportServices",package:"swift-nio-transport-services"),.product(name:"Crypto",package:"swift-crypto")]),.testTarget(name:"PlainSSHTests",dependencies:["PlainSSH"])])
