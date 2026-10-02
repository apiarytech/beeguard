# Development

## Layout on disk

beeguard is developed next to honeycomb:

```
apiarytech/
├── honeycomb/              TagDatabase, network API, store, connectors/plc4x
├── royaljelly/             IEC 61131-3 types
└── beeguard/               this repository
    └── examples/guard/field/   separate module: the PLC4X field server
```

`go.mod` replaces `github.com/apiarytech/honeycomb` with `../honeycomb`;
`examples/guard/field/go.mod` also replaces beeguard and the plc4x connector.
Remove or pin these before publishing.

## Build and test

```bash
go vet ./...
go test ./...
go test -tags sqlite ./cmd/beeguard          # the SQLite journal build
(cd examples/guard/field && go test ./...)  # the field server, with PLC4X
```

The tests need no devices or databases:

- `link` and `cmd/beeguard` start a real honeycomb HTTPS server with a
  development certificate and link to it with verification switched on.
- `examples/guard/field` polls the Modbus simulator through the real PLC4X
  driver and reads the values back over HTTPS.
- The SQL journal tests use SQLite in a temporary directory.

## Running the example

See [examples/guard/README.md](../examples/guard/README.md). Stop the
simulator or the field server to see the communication-failure alarm.

## Third-party licenses

`THIRD_PARTY_LICENSES.txt` must list every module linked into what you
distribute. Regenerate the files when dependencies change:

```bash
scripts/gen-third-party-licenses.sh                                            # default build
scripts/gen-third-party-licenses.sh -tags sqlite -o THIRD_PARTY_LICENSES-sqlite.txt
(cd examples/guard/field && ../../../scripts/gen-third-party-licenses.sh)    # field server
```

and update the table in [NOTICE.md](../NOTICE.md). A new dependency in the main
module needs a decision in [decisions.md](decisions.md): the default build is
kept free of third-party modules other than honeycomb and royaljelly.

## Working without network access

If `go` cannot reach `proxy.golang.org` or `sum.golang.org`:

1. Seed `go.sum` from sibling repositories whose checksums are already verified,
   e.g. `cat ../honeycomb/go.sum go.sum | sort -u`.
2. Resolve from the module cache: `GOPROXY=off go mod tidy`. Keep the checksum
   database enabled; it is only consulted for hashes missing from `go.sum`.
3. Run `go mod verify` once network access is back.

## Conventions

- Every source file starts with the dual-license header used across apiarytech.
- `alarm`, `evaluator` and `engine` stay free of I/O, goroutines and honeycomb;
  pass time in rather than reading the clock.
- New features that other systems use go through `api.Service` first, then
  through a transport such as `httpapi`.
- Tests that need time use explicit times, not `time.Sleep`, except where they
  exercise real goroutines or servers.
- New design decisions go in [decisions.md](decisions.md), and new external
  sources in its "Sources and originality" table.
