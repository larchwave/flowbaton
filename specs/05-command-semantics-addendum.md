# Command Semantics

This document records command behavior that is easy to implement incorrectly.
The production registry and parser remain the executable command inventory.

## 1. Links and navigation

`openLink` accepts a scalar URL or an object with `link`, optional `appId`,
`autoVerify`, and `browser`. The scalar form uses host defaults. Unknown object
fields and conflicting shapes are invalid.

`openBrowser` requires a URL and delegates browser startup to the active driver.
`waitForAnimationToEnd` polls until the hierarchy is stable or its finite budget
expires.

## 2. Travel

`travel` accepts an ordered list of latitude and longitude points plus an
optional speed. A route must contain at least one point.

Execution emits the first point once. Each later segment emits exactly 50 linear
steps, including the segment endpoint. A shared junction is emitted once. Delay
between emissions is derived from segment distance and speed. Invalid points or
non-positive speed fail before the first device mutation.

## 3. Application launch

`launchApp` follows `specs/06-launch-app-semantics.md`. Its argument map is
serialized in sorted key order. Supported values are string, Boolean, integer,
and floating-point.

## 4. Files

Commands that consume files receive paths prepared by the capability layer.
Execution must not reopen an authored relative path after preparation.

`addMedia` passes every prepared media path to the driver. `runScript` reads its
prepared script. `assertScreenshot` accepts a scalar image path or an object
with `path`, `thresholdPercentage`, and `cropOn`. `cropOn` supplies the selector
whose bounds define the screenshot crop.

## 5. Random input

Random input commands use the session input generator. Omitted length uses the
product default. A supplied length must be positive and within the documented
limit. Tests inject a generator and assert output shape rather than fixed random
values.

## 6. Optional element operations

For a command that supports `optional: true`, an ordinary missing-element result
may complete without failure. Cancellation, invalid configuration, transport
failure, and an unsupported platform operation still fail.

## 7. Retry and repeat

Retry conditions have finite attempt or time limits. Repeat without `times` or a
terminating condition is rejected by preflight. Nested commands retain their
own diagnostics and cancellation behavior.

## 8. Flow completeness gate

The command-manifest tests send the smallest and largest accepted authored shape
for every command through parse, prepare, evaluate, and execute. Any intentional
gap must be named in the test and must fail if the gap becomes stale.

## 9. Keys

`pressKey: HOME` sends the foreground application to the background without
terminating it; its state survives. On iOS the runner presses the device's home
button, so the flow's application is no longer in front afterwards: a following
`launchApp` with `stopApp: false` resumes it, and commands that read its
hierarchy before that are refused as "not in the foreground". `LOCK`, `POWER`
and the volume keys stay Android-only; preflight refuses them on iOS.

## 10. Log capture

`startLogCapture: NAME` (or `startLogCapture: {name: NAME, stream: STREAM}`)
opens one log stream and `stopLogCapture` closes it; the finished file is an
artifact of the stop command, named `NAME` plus the driver's extension, kept
in the run output directory next to the failure screenshots. `NAME` is a
basename: no path separators, no traversal. `stream` is `system` (the
default: the platform's own log) or `stdio` (the process's standard output
and error, §10.1); any other value is refused. A second `startLogCapture`
while one is open is refused, as is a `stopLogCapture` with none open. A
capture still open when the session ends is closed and its file kept under
its name, but no command links it.

The system stream is the flow's application where the platform can filter,
and the whole device where it cannot; the artifact metadata says which
(`scope: app` or `scope: device`), and which log it is (`source`):

| Platform | Source | Scope | Bound |
| --- | --- | --- | --- |
| Android | `logcat` | the application's process (`--pid`) | 16 MiB |
| iOS Simulator | `unified-log` | `process == CFBundleExecutable` | 16 MiB |
| iOS device | `syslog` (relay) | whole device | `FLOWBATON_IOS_DEVICE_LOG_LIMIT` |
| Web | none | refused at preflight | — |

None of these system sources carries the application's standard output or
standard error: on iOS a process that prints instead of logging leaves
nothing in the unified log. That is what the `stdio` stream is for. An
unknown application on the Simulator fails `startLogCapture` rather than
filtering for a process that cannot exist. `startLogCapture` completes only
once the stream has written its first bytes, so a stop that follows at once
still finds a file; a stream that never writes fails `startLogCapture`, and
a quiet application yields a small file, not an error. `stopLogCapture`
reports the file, its source, its scope and its size in the command's log
messages and in the artifact metadata of the commands document and the
detailed HTML report.

### 10.1 The stdio stream

`stream: stdio` captures what the application's process writes to its
standard output and standard error. Only a driver that launches the process
itself can bind those streams, so the stream exists on the iOS Simulator
only (`simctl launch --stdout --stderr`); a physical iOS device, Android and
Web refuse it at the `startLogCapture` step with the reason. Preflight
cannot tell a Simulator from a physical device, so that refusal is at the
step, not before the run.

The capture is pending until the flow launches its application:
`startLogCapture` must come before the `launchApp` whose output it wants.
Every launch of the flow's application while the capture is open is bound to
it; a launch of another application is an ordinary launch. A bound launch
always terminates a running copy of the application, even with
`stopApp: false`, because a running process has its streams bound elsewhere.
The finished file (`NAME.log`, `source: stdio`, `scope: app`) holds each
launch in order under marker lines `### launch N stdout` and
`### launch N stderr`; the stderr section appears only when the process
wrote to it. The file is capped at 16 MiB when it is assembled
(`truncated: true` in the metadata); bytes the process has not flushed by
`stopLogCapture` are not in it. A `stopLogCapture` that saw no launch of the
application fails and leaves no file; when the session cleans up an unused
capture after a flow failed earlier, that flow's own error is the one
reported.
