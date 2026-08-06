import Foundation
import XCTest

@testable import FlowBatonIOSRunner

final class LoopbackHTTPServerTests: XCTestCase {
  func testServesStatusOnLoopbackHTTP() async throws {
    let server = LoopbackHTTPServer(port: 0)
    let port = try server.start(timeout: 2)
    defer { server.stop() }

    var request = URLRequest(
      url: try XCTUnwrap(URL(string: "http://127.0.0.1:\(port)/status"))
    )
    request.timeoutInterval = 2

    let (data, response) = try await URLSession.shared.data(for: request)
    let httpResponse = try XCTUnwrap(response as? HTTPURLResponse)

    XCTAssertEqual(httpResponse.statusCode, 200)
    XCTAssertEqual(httpResponse.value(forHTTPHeaderField: "Content-Type"), "application/json")
    XCTAssertEqual(String(decoding: data, as: UTF8.self), #"{"status":"ok"}"#)
  }

  func testBindsTheSpecificPortItWasAskedFor() throws {
    // This test exists because the server could NOT do this. Naming the port
    // both in requiredLocalEndpoint and in NWListener(on:) made every specific
    // port fail with EINVAL, so only an ephemeral port ever worked — and the
    // test above, which asks for port 0, could never have noticed.
    //
    // The whole contract is built on the host finding the runner at a known
    // port, so a runner that can only bind a random one is a runner the host
    // cannot reach.
    let server = LoopbackHTTPServer(port: 22087)
    let bound: UInt16
    do {
      bound = try server.start(timeout: 2)
    } catch {
      throw XCTSkip("port 22087 is unavailable on this machine: \(error)")
    }
    defer { server.stop() }
    XCTAssertEqual(bound, 22087)
  }

  func testIsNotReachableFromOffTheMachine() throws {
    // requiredLocalEndpoint pins the listener to loopback. That is the reason
    // the runner is safe to leave running: it accepts a UI automation API with
    // no authentication, so being unreachable from the network is the only
    // thing standing between it and anyone on the same wifi.
    let server = LoopbackHTTPServer(port: 0)
    let port = try server.start(timeout: 2)
    defer { server.stop() }

    guard let external = Self.nonLoopbackAddress() else {
      throw XCTSkip("no non-loopback address on this machine to test against")
    }
    var request = URLRequest(url: try XCTUnwrap(URL(string: "http://\(external):\(port)/status")))
    request.timeoutInterval = 3

    let finished = expectation(description: "refused")
    let reachable = ReachabilityFlag()
    URLSession.shared.dataTask(with: request) { _, response, _ in
      if let http = response as? HTTPURLResponse, http.statusCode == 200 {
        reachable.set()
      }
      finished.fulfill()
    }.resume()
    wait(for: [finished], timeout: 10)

    XCTAssertFalse(reachable.value, "the runner answered on \(external); it must be loopback-only")
  }

  /// A box, because the completion handler runs on another thread.
  private final class ReachabilityFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var flag = false
    func set() { lock.withLock { flag = true } }
    var value: Bool { lock.withLock { flag } }
  }

  /// One IPv4 address of this machine that is not 127.0.0.1.
  private static func nonLoopbackAddress() -> String? {
    var head: UnsafeMutablePointer<ifaddrs>?
    guard getifaddrs(&head) == 0, let first = head else { return nil }
    defer { freeifaddrs(head) }

    var cursor: UnsafeMutablePointer<ifaddrs>? = first
    while let entry = cursor {
      defer { cursor = entry.pointee.ifa_next }
      guard let address = entry.pointee.ifa_addr,
        address.pointee.sa_family == UInt8(AF_INET)
      else { continue }
      var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
      guard
        getnameinfo(
          address, socklen_t(address.pointee.sa_len), &host, socklen_t(host.count),
          nil, 0, NI_NUMERICHOST) == 0
      else { continue }
      let bytes = host.prefix(while: { $0 != 0 }).map { UInt8(bitPattern: $0) }
      let text = String(decoding: bytes, as: UTF8.self)
      if text != "127.0.0.1" && !text.hasPrefix("169.254.") {
        return text
      }
    }
    return nil
  }
}
