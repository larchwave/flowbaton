# CLI and Tooling Specification

## 1. Command surface

The `flowbaton` executable dispatches these user commands:

- `check-syntax FILE|-`
- `test FILE|DIR...`
- `record [--local] FLOW [OUTPUT]`
- `list-devices [-p ios|android|web]`
- `start-device -p ios|android --device ID [platform options]`
- `hierarchy -p ios|android [--device ID] [--csv]`
- `query -p ios|android [--device ID] EXPRESSION`
- `bugreport -p ios|android [--device ID] [--output PATH]`
- `driver-setup [-p ios|android]`
- `mcp [--base-dir DIR] [--no-viewer] [--viewer-port PORT]`
- `serve [options]`
- `db apply-schema --database-url URL`
- `auth keygen|cert-map [options]`
- `generate-completion [bash|zsh]`

Unknown commands and malformed options exit with status 2. A well-formed command
that fails during discovery, setup, execution, or reporting exits with status 1.
`driver-setup` selects iOS when the platform option is omitted.
`start-device` accepts the documented OS version, locale, model, system-image,
and force-create options. It does not return until the exact selected target is
ready.

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

`--base-dir` confines linked flow and resource resolution to the canonical
directory supplied by the operator. Viewer state is read-only and its port must
be explicit when the default is unsuitable.

## 7. Remote DeviceSession service

`flowbaton serve` hosts Integration v1 and DeviceSession v1 over TLS 1.3 with
mutual certificate authentication. It requires a PostgreSQL URL, server
certificate and private key, client CA, FlowBaton Ed25519 signing key, stable
node ID, HTTPS public address, and strict JSON device inventory.

The inventory is bounded, rejects duplicate resource IDs and capabilities, and
must agree with each constructed driver's platform capability document. A live
process owns a database-issued node epoch. A second process using that node ID
is refused while the first lease is live; a later incarnation receives a
higher epoch, and old claims cannot start or finish work.

Node and device rows remain unavailable for acquisition until every configured
driver opens successfully. The TLS listener publishes Integration v1 before the
first driver opens, but its readiness route stays closed until node activation.
One process-level heartbeat renews the node lease independently of worker load.
Device work uses claim generations, the worker claim lifetime must exceed the
device-operation timeout, and an input already marked executing is never placed
back on the pending queue.
