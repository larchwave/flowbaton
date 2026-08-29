import Foundation
import Network

public enum LoopbackHTTPServerError: Error, Equatable {
  case startupTimedOut
  case failedToStart(String)
}

public final class LoopbackHTTPServer: @unchecked Sendable {
  private let requestedPort: NWEndpoint.Port
  private let queue = DispatchQueue(label: "dev.larchwave.flowbaton.driver.http")
  private let lock = NSLock()
  private var listener: NWListener?
  /// Answers a parsed request. Injected so the transport can be exercised
  /// without an automation, and so the automation can be replaced without
  /// touching the socket code.
  private let handler: @Sendable (HTTPRequest) -> HTTPResponse

  /// Serves only /status for hosts that need runner health without automation.
  /// `runner` is the id the host launched this process with (`RunnerIdentity`).
  public convenience init(port: UInt16 = 22087, runner: String? = nil) {
    self.init(port: port) { request in
      StatusEndpoint.route(method: request.method, path: request.path, runner: runner)
    }
  }

  public convenience init(
    port: UInt16 = 22087, automation: any DeviceAutomation, runner: String? = nil
  ) {
    let router = RequestRouter(automation: automation, runner: runner)
    self.init(port: port) { router.route($0) }
  }

  public init(port: UInt16 = 22087, handler: @escaping @Sendable (HTTPRequest) -> HTTPResponse) {
    requestedPort = NWEndpoint.Port(rawValue: port) ?? .any
    self.handler = handler
  }

  public func start(timeout: TimeInterval) throws -> UInt16 {
    let parameters = NWParameters.tcp
    // The port is given to NWListener ONCE, via `on:`. Naming it here as well
    // made every specific port fail with EINVAL — the two ways of saying it
    // conflict — which meant the server could only ever bind an ephemeral
    // port, never the 22087 the contract is built around. `.any` here keeps
    // this line doing its real job: pinning the interface to loopback so the
    // runner is unreachable from off the machine.
    parameters.requiredLocalEndpoint = .hostPort(
      host: NWEndpoint.Host("127.0.0.1"),
      port: .any
    )

    let listener = try NWListener(using: parameters, on: requestedPort)
    let startup = StartupSignal()

    listener.stateUpdateHandler = { state in
      switch state {
      case .ready:
        if let port = listener.port?.rawValue {
          startup.complete(.success(port))
        } else {
          startup.complete(.failure(.failedToStart("listener became ready without a port")))
        }
      case .failed(let error):
        startup.complete(.failure(.failedToStart(String(describing: error))))
      case .cancelled:
        startup.complete(.failure(.failedToStart("listener cancelled before becoming ready")))
      default:
        break
      }
    }
    listener.newConnectionHandler = { [weak self] connection in
      self?.serve(connection)
    }

    lock.withLock {
      self.listener?.cancel()
      self.listener = listener
    }
    listener.start(queue: queue)

    guard startup.wait(timeout: timeout) else {
      stop()
      throw LoopbackHTTPServerError.startupTimedOut
    }

    return try startup.result.get()
  }

  public func stop() {
    lock.withLock {
      listener?.cancel()
      listener = nil
    }
  }

  private func serve(_ connection: NWConnection) {
    connection.start(queue: queue)
    receive(connection, accumulated: Data())
  }

  /// Reads until a whole request has arrived.
  ///
  /// Requests may carry bodies, and TCP may deliver them in pieces. The loop
  /// keeps appending and re-parsing until
  /// the parser stops asking for more.
  private func receive(_ connection: NWConnection, accumulated: Data) {
    connection.receive(minimumIncompleteLength: 1, maximumLength: 65_536) {
      [weak self] data, _, isComplete, error in
      guard let self else {
        connection.cancel()
        return
      }
      var buffer = accumulated
      if let data { buffer.append(data) }

      guard error == nil, !buffer.isEmpty else {
        connection.cancel()
        return
      }

      switch HTTPRequestParser.parse(buffer) {
      case .parsed(let request):
        self.respond(connection, self.handler(request))
      case .needMoreData:
        if isComplete {
          // The peer closed without finishing. Nothing more is coming, so
          // waiting for it would hold the connection open forever.
          self.respond(
            connection,
            RequestRouter.errorResponse(.precondition("request ended before it was complete")))
          return
        }
        self.receive(connection, accumulated: buffer)
      case .malformed(let reason):
        self.respond(connection, RequestRouter.errorResponse(.precondition(reason)))
      }
    }
  }

  private func respond(_ connection: NWConnection, _ response: HTTPResponse) {
    let headers = [
      "HTTP/1.1 \(response.statusCode) \(Self.reason(for: response.statusCode))",
      "Content-Type: \(response.contentType)",
      "Content-Length: \(response.body.count)",
      "Connection: close",
      "",
      "",
    ].joined(separator: "\r\n")
    var payload = Data(headers.utf8)
    payload.append(response.body)

    connection.send(
      content: payload,
      completion: .contentProcessed { _ in
        connection.cancel()
      })
  }

  private static func reason(for status: Int) -> String {
    switch status {
    case 200: return "OK"
    case 400: return "Bad Request"
    case 404: return "Not Found"
    case 408: return "Request Timeout"
    case 500: return "Internal Server Error"
    default: return "Unknown"
    }
  }
}

private final class StartupSignal: @unchecked Sendable {
  private let semaphore = DispatchSemaphore(value: 0)
  private let lock = NSLock()
  private var storedResult: Result<UInt16, LoopbackHTTPServerError>?

  var result: Result<UInt16, LoopbackHTTPServerError> {
    lock.withLock {
      storedResult ?? .failure(.failedToStart("listener produced no startup result"))
    }
  }

  func complete(_ result: Result<UInt16, LoopbackHTTPServerError>) {
    let didComplete = lock.withLock {
      guard storedResult == nil else { return false }
      storedResult = result
      return true
    }
    if didComplete {
      semaphore.signal()
    }
  }

  func wait(timeout: TimeInterval) -> Bool {
    semaphore.wait(timeout: .now() + timeout) == .success
  }
}
