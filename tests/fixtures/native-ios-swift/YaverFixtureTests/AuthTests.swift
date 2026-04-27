import XCTest
@testable import YaverFixture

final class AuthTests: XCTestCase {
    func testAcceptsValidHardcodedCredentials() {
        XCTAssertTrue(Auth.authenticate(username: "admin", password: "admin"))
    }

    func testRejectsWrongPassword() {
        XCTAssertFalse(Auth.authenticate(username: "admin", password: "wrong"))
    }

    func testRejectsUnknownUser() {
        XCTAssertFalse(Auth.authenticate(username: "intruder", password: "admin"))
    }

    func testRejectsEmptyInputs() {
        XCTAssertFalse(Auth.authenticate(username: "", password: ""))
    }
}
