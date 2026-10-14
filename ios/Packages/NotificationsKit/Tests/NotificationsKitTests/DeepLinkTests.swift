import XCTest
@testable import NotificationsKit

final class DeepLinkTests: XCTestCase {
    func testParsesServerDeepLinks() {
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://vehicle/v1")!), .vehicle(id: "v1"))
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://vehicle/v1/charging")!), .charging(vehicleID: "v1"))
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://vehicle/v1/charges/c9")!), .charge(vehicleID: "v1", chargeID: "c9"))
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://vehicle/v1/trips/t3")!), .trip(vehicleID: "v1", tripID: "t3"))
        XCTAssertNil(DeepLink(url: URL(string: "https://vehicle/v1")!))
        XCTAssertNil(DeepLink(url: URL(string: "milesight://elsewhere")!))
    }

    func testAuthCallback() {
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://auth/callback?code=abc&state=xyz")!),
                       .authCallback(code: "abc", state: "xyz", error: nil))
        XCTAssertEqual(DeepLink(url: URL(string: "milesight://auth/callback?error=access_denied")!),
                       .authCallback(code: nil, state: nil, error: "access_denied"))
    }

    func testPushPayload() {
        let link = DeepLink(pushPayload: ["deep_link": "milesight://vehicle/v1/trips/t1", "vehicle_id": "v1"])
        XCTAssertEqual(link, .trip(vehicleID: "v1", tripID: "t1"))
        XCTAssertEqual(DeepLink(pushPayload: ["vehicle_id": "v2"]), .vehicle(id: "v2"))
        XCTAssertNil(DeepLink(pushPayload: [:]))
    }

    func testTokenHex() {
        XCTAssertEqual(PushRegistrar.hex(Data([0x0f, 0xa0, 0xff])), "0fa0ff")
    }
}
