# Application Launch Semantics

## 1. Operation order

`launchApp` performs enabled operations in this order:

1. clear keychain;
2. clear application state;
3. apply permissions;
4. stop the running application when `stopApp` is true;
5. launch the application with typed arguments.

`stopApp` defaults to true. An operation disabled by the authored command is
omitted without changing the order of the remaining operations.

## 2. Validation

The evaluator resolves the effective application identifier, validates every
permission value and launch argument, and sorts argument keys before the first
driver call. Invalid input cannot leave the device partly reset.

## 3. Failure behavior

The first failed operation stops the sequence and returns its error. Later
operations do not run. Cancellation is preserved. A platform that cannot
perform an enabled operation returns the shared unsupported sentinel.

## 4. Android

Android clear-state, permissions, stop, and launch operations use ADB or the
managed agent according to the device-driver contract. Launch arguments are
encoded as typed Java extras for every supported value.

## 5. iOS Simulator

iOS clear-state, permissions, stop, and launch operations use `simctl`.
Application installation after a state clear must finish before permissions are
applied. Both Boolean values use an undashed key followed by the Boolean value.
Non-Boolean values use a dashed key followed by the value.

## 6. Web

For a web flow, the URL is the effective application identifier. Stop navigates
the current page to `about:blank`, and launch navigates through the web driver.
Mobile-only reset or permission options return the shared unsupported sentinel.

## 7. Tests

Tests must cover the full ordered sequence, every optional omission, sorted
arguments, typed values, first-error stopping, cancellation, and unsupported
platform operations.
