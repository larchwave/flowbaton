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
  @State private var confirmingEnd = false
  @State private var ended = false

  var body: some View {
    if continued {
      ScrollFixturePage()
    } else {
      VStack(spacing: 32) {
        Text("Settle fixture")
          .font(.title)
          .accessibilityIdentifier("fixture.title")
        DecorativeWaveform()
          .frame(height: 160)
          .accessibilityHidden(true)
        if ended {
          Text("Session ended")
            .accessibilityIdentifier("fixture.alert.ended")
        }
        Spacer()
        // An app-owned alert with a destructive action, the shape of issue
        // #17: a tap on its button must run that action, not land beneath it.
        Button("End session") {
          confirmingEnd = true
        }
        .accessibilityIdentifier("fixture.alert.show")
        .alert("End this session?", isPresented: $confirmingEnd) {
          Button("End without saving", role: .destructive) {
            ended = true
          }
          Button("Keep going", role: .cancel) {}
        }
        Button("Continue") {
          continued = true
        }
        .buttonStyle(.borderedProminent)
        .accessibilityIdentifier("fixture.continue")
      }
      .padding(20)
    }
  }
}

/// The page behind Continue reproduces issue #12 as the reporter saw it: a
/// card in a ScrollView with a plain text Link, rendered at the largest
/// accessibility text size, sitting where a downward scroll drag starts (the
/// middle of the screen plus a quarter of its height). A marker further down
/// only a real scroll brings on screen. The link points back at this app
/// (scheme `settlefixture`, declared in project.yml), so an activation shows
/// up in the hierarchy as `fixture.linkActivated` instead of as another app
/// in the foreground.
struct ScrollFixturePage: View {
  @State private var linkActivated = false

  var body: some View {
    GeometryReader { geometry in
      ScrollView {
        VStack(alignment: .leading, spacing: 24) {
          Text("Continued")
            .font(.headline)
            .accessibilityIdentifier("fixture.continued")
          if linkActivated {
            Text("Link activated")
              .accessibilityIdentifier("fixture.linkActivated")
          }
          Color.clear.frame(height: geometry.size.height * 0.4)
          VStack(alignment: .leading, spacing: 16) {
            Text("Subscription")
              .font(.headline)
            if let destination = URL(string: "settlefixture://link") {
              Link("Manage the account", destination: destination)
                .accessibilityIdentifier("fixture.link")
            }
          }
          .padding(20)
          .frame(maxWidth: .infinity, alignment: .leading)
          .background(Color.accentColor.opacity(0.15), in: RoundedRectangle(cornerRadius: 16))
          Color.clear.frame(height: geometry.size.height)
          Text("End of the page")
            .accessibilityIdentifier("fixture.end")
          // Three option cards, each shorter than the screen and taller than
          // half of it, so a scroll step of the default size can carry the
          // second one across the screen between two observations (issue #16).
          ForEach(Array(Self.cardTitles.enumerated()), id: \.offset) { index, title in
            Button {
            } label: {
              VStack(alignment: .leading, spacing: 12) {
                Text(title)
                  .font(.headline)
                Text(Self.cardBody)
              }
              .padding(20)
              .frame(maxWidth: .infinity, alignment: .leading)
              .background(Color.accentColor.opacity(0.15), in: RoundedRectangle(cornerRadius: 16))
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("fixture.card.\(index + 1)")
          }
        }
        .padding(20)
      }
    }
    .dynamicTypeSize(.accessibility5)
    .onOpenURL { _ in linkActivated = true }
  }

  static let cardTitles = ["Grow audience", "Launch a product", "Publish faster"]
  static let cardBody =
    "Every post, clip and note goes out on the schedule you set once, and the numbers "
    + "come back in one place."
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
