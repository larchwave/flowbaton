# Core Engine Specification

## 1. Flow document

A flow is a YAML metadata document, `---`, and a YAML command sequence. The
metadata may define an application target, tags, environment values, lifecycle
hooks, platform settings, and JavaScript startup files.

`appId` selects a mobile application. `url` selects a web target and becomes the
effective application identifier for that run. A flow that has neither is
invalid unless its calling context supplies the target.

The parser preserves file, line, and column data for diagnostics. Unknown
commands, conflicting command shapes, and invalid scalar types fail during
preparation.

## 2. Execution pipeline

Execution follows this order:

1. discover and parse flows;
2. resolve files and environment values;
3. perform capability preflight without device mutation;
4. acquire one device session per shard;
5. run flow-start hooks;
6. evaluate and execute commands in authored order;
7. run flow-complete hooks;
8. write reports and debug artifacts;
9. release the session.

Cancellation stops pending work and is never converted into an ordinary command
failure. Cleanup still runs for resources already acquired.

## 3. Commands

The production registry is the executable list of accepted command keys. Every
registered command must provide:

- parser coverage for each accepted YAML shape;
- recursive capability coverage;
- evaluator coverage for expressions and environment expansion;
- execution coverage against an injected driver or service;
- stable diagnostics for invalid configuration.

Nested commands used by conditions, loops, retries, and subflows follow the same
pipeline. Optional element commands may ignore an ordinary missing-element
result, but must not swallow cancellation or platform failures.

## 4. Element lookup and timing

Element operations use an injected clock. Production uses real time; tests use
a deterministic clock. Lookup, polling, settle, animation, and retry budgets
must remain explicit constants or request fields. No command may read wall time
directly when an injected clock is available.

Element selection is handled by `internal/matching`. Drivers return platform
hierarchy data; the engine owns selector logic, relation filtering, and the
choice of actionable coordinates.

## 5. Scripts and environment

The JavaScript runtime is isolated per execution session. `runScript` loads a
prepared file and `evalScript` evaluates authored source. Script values may read
the current FlowBaton environment and publish only the documented session
values.

Reserved FlowBaton environment keys are set by the host after user and workspace
values are merged. User input cannot override a reserved key.

## 6. AI commands

AI commands require a configured provider and a screenshot-capable session.
Missing provider credentials fail before a provider call. Every provider call
receives the current PNG screenshot and the authored prompt or extraction
schema. Provider output is treated as untrusted input and must be validated
before it changes command state.

## 7. Determinism

Map-backed output is sorted before serialization. Random input uses an injected
generator. Tests must not depend on operating-system directory order, map order,
real clock progress, or ambient environment values.
