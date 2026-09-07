import SwiftUI

/// The settle fixture: a screen that never stops moving for a camera and never
/// changes for accessibility.
///
/// The waveform redraws every frame and is hidden from the accessibility tree;
/// the Continue button sits still and is enabled from the first frame. A flow
/// that launches this app, asserts the button and taps it exercises the case
/// from issue #7, where a pixel-static gate starved hierarchy settling. This
/// target is never packaged: it has its own scheme so the runner's Products
/// directory stays clean.
@main
struct SettleFixtureApp: App {
  var body: some Scene {
    WindowGroup {
      SettleFixtureScreen()
    }
  }
}

struct SettleFixtureScreen: View {
  @State private var continued = false

  var body: some View {
    VStack(spacing: 32) {
      Text("Settle fixture")
        .font(.title)
        .accessibilityIdentifier("fixture.title")
      DecorativeWaveform()
        .frame(height: 160)
        .accessibilityHidden(true)
      if continued {
        Text("Continued")
          .font(.headline)
          .accessibilityIdentifier("fixture.continued")
      }
      Spacer()
      Button("Continue") {
        continued = true
      }
      .buttonStyle(.borderedProminent)
      .accessibilityIdentifier("fixture.continue")
    }
    .padding(20)
  }
}

/// A sine wave whose phase follows the wall clock, so every frame differs from
/// the one before it whether or not the system honours Reduce Motion.
struct DecorativeWaveform: View {
  var body: some View {
    TimelineView(.animation) { context in
      Canvas { graphics, size in
        let phase = context.date.timeIntervalSince1970 * 2
        var path = Path()
        let midY = size.height / 2
        path.move(to: CGPoint(x: 0, y: midY))
        for step in stride(from: 0, through: Int(size.width), by: 2) {
          let x = Double(step)
          let y = midY + sin(x / 24 + phase) * size.height / 3
          path.addLine(to: CGPoint(x: x, y: y))
        }
        graphics.stroke(path, with: .color(.accentColor), lineWidth: 4)
      }
    }
  }
}
