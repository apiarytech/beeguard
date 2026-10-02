# Example: simulated guard

A complete setup on one machine, in three processes:

```
 modbus_sim ──Modbus TCP──► field (PLC4X + honeycomb) ──HTTPS :9443──► beeguard ──HTTPS :8443 tags / :8444 API
```

| File | Used by | Purpose |
|---|---|---|
| [io.json](io.json) | field | Modbus addresses polled by the PLC4X connector |
| [links.json](links.json) | beeguard | The field database and the tags beeguard links to |
| [alarms.json](alarms.json) | beeguard | The alarms |
| `dev/` | both | Self-signed development certificate, created on first run (do not commit) |

## Run it

From the repository root, in three terminals:

```bash
go run ./cmd/modbus_sim
```

```bash
cd examples/guard/field
go run . -io ../io.json -dev-cert ../dev -port 9443 -token field-secret
```

```bash
FIELD_TOKEN=field-secret go run ./cmd/beeguard -links examples/guard/links.json \
    -alarms examples/guard/alarms.json -dev-cert examples/guard/dev -token beeguard-secret
```

On Windows PowerShell set the variable first: `$env:FIELD_TOKEN = "field-secret"`.

Then ask beeguard for the active alarms:

```bash
curl --cacert examples/guard/dev/cert.pem -H "Authorization: Bearer beeguard-secret" \
     https://127.0.0.1:8444/v1/alarms/active
```

The field server is a separate Go module (`field/go.mod`) so that beeguard
itself does not depend on PLC4X.

## The simulated device

`cmd/modbus_sim` serves one simulated guard device over Modbus TCP on `127.0.0.1:5020`:

| Tag | Address | Behaviour |
|---|---|---|
| `Guard1.Temp` | `holding-register:1:REAL` | 31.5..39.5 °C sine, 2 minute period |
| `Guard1.Weight` | `holding-register:3:REAL` | 42 kg; drops 2.5 kg in 5 s at 90 s of every 3 minutes |
| `Guard1.Humidity` | `holding-register:5:REAL` | 55..65 % |
| `Guard1.Lid` | `coil:1:BOOL` | open from 30 s to 40 s of every minute |

## The alarms

| Alarm | Condition | Expect |
|---|---|---|
| `Guard1.TempHigh` | Temp > 38 °C, 0.5 °C deadband, 3 s delays | about 17 s after start |
| `Guard1.TempLow` | Temp < 32 °C | about 77 s after start |
| `Guard1.Swarm` | Weight falling faster than 0.2 kg/s, latched | at 90 s; needs ack and reset |
| `Guard1.LidOpen` | Lid open for 2 s | at 32 s of every minute; chatters after three |
| `Guard1.CommFail` | Temp quality bad for 5 s | stop the simulator or the field server to see it |

beeguard follows the field database's change feed, so alarms react within milliseconds of the field server seeing a change; the field server itself reads the device every 500 ms (`interval` in io.json).
