import XCTest
@testable import YaverTV

final class PrivateVPSURLTests: XCTestCase {
    func testNormalizesURLAndQRPAYloads() {
        XCTAssertEqual(Backend.normalizedPrivateVPSURL(" https://vps.example.com/yaver/// ")?.absoluteString,
                       "https://vps.example.com/yaver")
        XCTAssertEqual(Backend.privateVPSURL(fromQRCode: "yaver://private-vps?url=https%3A%2F%2Fvps.example.com%2Fyaver")?.absoluteString,
                       "https://vps.example.com/yaver")
        XCTAssertEqual(Backend.privateVPSURL(fromQRCode: "{\"privateVpsUrl\":\"https://vps.example.com\"}")?.absoluteString,
                       "https://vps.example.com")
    }

    func testRejectsCredentialsAndAmbiguousParameters() {
        XCTAssertNil(Backend.normalizedPrivateVPSURL("https://user:secret@vps.example.com"))
        XCTAssertNil(Backend.normalizedPrivateVPSURL("https://vps.example.com?token=secret"))
        XCTAssertNil(Backend.normalizedPrivateVPSURL("http://vps.example.com"))
        XCTAssertNil(Backend.normalizedPrivateVPSURL("ftp://vps.example.com"))
    }
}
