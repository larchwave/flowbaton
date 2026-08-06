# CLI and Tooling Specification

## 1. Command surface

The `flowbaton` executable dispatches these user commands:

- `check-syntax FILE|-`
- `test FILE|DIR...`
- `record [--local] FLOW [OUTPUT]`
- `list-devices [-p ios|android|web]`
- `start-device -p ios|android --device ID`
- `hierarchy -p ios|android [--device ID] [--csv]`
- `query -p ios|android [--device ID] EXPRESSION`
- `bugreport -p ios|android [--device ID] [--output PATH]`
- `driver-setup [-p ios|android] [--apple-team-id ID]`
- `mcp [--no-viewer]`
- `generate-completion bash|zsh`

Unknown commands and malformed options exit with status 2. A well-formed command
that fails during discovery, setup, execution, or reporting exits with status 1.
`driver-setup` selects iOS when the platform option is omitted.

## 2. Flow discovery

A file input runs directly. A directory input scans only its top level for YAML
flows. Workspace configuration is discovered from `config.yaml` or `config.yml`
unless `--config` supplies an explicit path.

CLI and workspace tag filters are merged. `executionOrder.flowsOrder` names
flows that run first; remaining flows use stable path order. Unknown or repeated
flow names are invalid.

## 3. Test options

The `test` command supports platform and device selection, environment entries,
tag filters, reporting, debug output, continuous mode, sharding, driver setup,
web options, and configured AI features.

`--shard-split N` assigns flow index `i` to shard `i mod N` and requires enough
devices. A sequential flow sequence cannot be split across shards.

## 4. Reporting

Supported report modes are `NOOP`, `JUNIT`, `HTML`, and `HTML-DETAILED`. `NOOP`
is the default. Reports and debug artifacts use deterministic paths and contain
one result per executed flow and command.

Debug output may include command metadata, logs, screenshots, video, and a JSON
artifact manifest. Output writers must reject path escapes.

## 5. Environment order

Values are applied in this order:

1. process environment;
2. workspace configuration;
3. CLI `-e` entries;
4. flow metadata;
5. host-owned reserved values.

Later entries win. Reserved FlowBaton keys always come from the host.

## 6. MCP

`flowbaton mcp` serves tools over standard input and output. Tool handlers use
the same parser, setup, and execution services as the CLI. Protocol output must
not be mixed with human log text.
