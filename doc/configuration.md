# Configuration

`cmd/beeguard` reads two JSON files: the links to the honeycomb databases that
hold the source tags, and the alarm definitions. [examples/guard](../examples/guard/)
has a working set.

## Links (`-links`)

```json
{
  "databases": [{
    "id": "field1",
    "url": "https://field-pc:8443",
    "tokenEnv": "FIELD1_TOKEN",
    "caFile": "certs/field1.pem",
    "timeout": "5s",
    "tags": ["Guard1.Temp", {"tag": "Guard1.Lid", "remote": "Hive1.LidSwitch"}]
  }]
}
```

| Key | Default | Meaning |
|---|---|---|
| `id` | required | name of the linked database |
| `url` | required | `https://host:port` of its honeycomb tag API |
| `token` / `tokenEnv` | | bearer token, or the environment variable that holds it (preferred) |
| `caFile` | | PEM certificate to trust in addition to the system roots |
| `timeout` | `5s` | per-request timeout |
| `tags` | | names linked under the same name, or `{"tag", "remote"}` to rename |

Each linked tag becomes a local remote-alias tag that alarms use as `source`.

## Alarms (`-alarms`)

```json
{
  "alarms": [{
    "id": "Guard1.TempHigh",
    "description": "Guard 1 brood temperature high",
    "source": "Guard1.Temp",
    "kind": "high",
    "limit": 38, "deadband": 0.5,
    "onDelay": "3s", "offDelay": "3s",
    "severity": 700, "ackRequired": true, "latched": false,
    "maxShelve": "8h", "chatterCount": 3, "chatterWindow": "5m"
  }]
}
```

| Key | Default | Meaning |
|---|---|---|
| `id` | required | unique alarm ID |
| `description` | | shown in events and the journal |
| `source` | required | linked tag the condition is evaluated on |
| `kind` | required | `digital`, `high`, `low`, `rate-of-rise`, `rate-of-fall`, `bad-quality` |
| `limit` | 0 | high/low limit, or rate in units per second |
| `deadband` | 0 | high/low hysteresis on return to normal |
| `invert` | false | digital: alarm when the value is 0 |
| `period` | | rates: sampling period (required for rates) |
| `onDelay`, `offDelay` | 0 | condition delays |
| `severity` | 500 | 1..1000 |
| `ackRequired` | true | |
| `latched` | false | stays in alarm until acknowledged and reset |
| `maxShelve` | 0 | longest shelve; 0 = cannot be shelved |
| `chatterCount`, `chatterWindow` | 0 | chatter detection; 0 = off |

Durations are Go durations (`500ms`, `15s`, `8h`). Unknown keys are rejected
in both files. See [conditions.md](conditions.md) and [alarm-model.md](alarm-model.md).

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-links` | `links.json` | linked databases and tags |
| `-alarms` | `alarms.json` | alarm definitions |
| `-journal` | `beeguard.jsonl` | `.jsonl` file; `.db`/`.sqlite` with `-tags sqlite`; empty = memory |
| `-tick` | `1s` | longest wait between evaluations; delays, shelves and rates are evaluated exactly when due |
| `-feed-wait` | `25s` | how long one change-feed request waits before it is renewed |
| `-poll` | `1s` | how often linked tags are read from a database without a change feed; retry interval after a failed feed request |
| `-source-time-skew` | `5s` | evaluate delays and rates in device time when it is within this of the server clock; `0` uses arrival time |
| `-user` | `hmi` | user recorded for tag commands that name none |
| `-tags-port` | `8443` | honeycomb tag API (status and command tags); empty disables it |
| `-api-port` | `8444` | beeguard API; empty disables it |
| `-cert`, `-key` | | TLS certificate and key for both APIs |
| `-dev-cert` | | directory for a self-signed development certificate, used when `-cert` is empty |
| `-token` | | bearer token for both APIs |
| `-token-env` | `BEEGUARD_TOKEN` | variable read when `-token` is empty (preferred: keeps the token out of process lists) |
| `-log-format` | `text` | `text` or `json` |
| `-log-level` | `info` | `debug`, `info`, `warn` or `error` |

## Field server (`examples/guard/field`)

| Flag | Default | Meaning |
|---|---|---|
| `-io` | `io.json` | honeycomb plc4x connector configuration |
| `-port` | `8443` | HTTPS port of its tag API |
| `-cert`, `-key`, `-dev-cert` | | as for beeguard |
| `-token`, `-token-env` | `FIELD_TOKEN` | bearer token beeguard must send |
| `-plc4x-log` | `error` | PLC4X driver log level |

It creates a tag for every binding in `io.json`, typed from the address
(`holding-register:1:REAL` → `REAL`).
