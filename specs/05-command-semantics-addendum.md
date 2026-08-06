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
