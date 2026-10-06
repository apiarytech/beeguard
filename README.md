# beeguard - ISA-18.2 Alarm & Event Server in Go

`beeguard` is an alarm and event server for operational technology, built on
[honeycomb](https://github.com/apiarytech/honeycomb) tag databases. It links
to the honeycomb databases that hold field values, evaluates alarms on them
with the ISA-18.2 / IEC 62682 alarm state model, journals every event, and
offers its alarms through two HTTPS APIs: honeycomb's tag API (status and
command tags) and a beeguard API for alarms, commands and the journal.

The default build depends only on honeycomb and royaljelly; there are no other
third-party modules. Design and structure are documented in [doc/](doc/README.md).

## Features

- **ISA-18.2 states A-H**: normal, unacknowledged, acknowledged, returned to
  normal unacknowledged, latched, shelved, suppressed by design and out of service.
- **Acknowledge and latching** per alarm; reset only after the condition clears
  and the alarm is acknowledged.
- **Shelving** that is operator only and always time limited, with optional
  one-shot shelving.
- **Re-announcement**: a hidden alarm whose condition is still active comes
  back unacknowledged.
- **Conditions**: digital, high, low (with deadband), rate of rise, rate of
  fall and bad quality, with on-delay and off-delay. Bad-quality data holds the
  condition, and an unreachable database counts as bad quality.
- **Priority** from a 1..1000 severity, **chattering detection**, **alarm counts**.
- **Journal** as a JSON Lines file (default), in memory, or in SQLite with
  `-tags sqlite`, with the device time of the value behind each event,
  retention by age (`"journal": {"retention": "8760h"}` in the alarms file)
  and paging from the oldest entry, to read a whole day for a report.
- **APIs**: a transport-neutral Go service interface ([api](api/)) ready to be
  wrapped as a microservice, served over HTTPS/JSON ([api/httpapi](api/httpapi/)).
- **Logging** through the standard library's `log/slog`, as text or JSON.

## How it fits together

```
 devices ──► field honeycomb ──HTTPS──► beeguard ──HTTPS──► HMIs, services
            (e.g. PLC4X connector)     links, alarms,      tag API + beeguard API
                                       journal
```

beeguard does not talk to devices. Any program that serves a honeycomb
TagDatabase can be the field side; [examples/guard/field](examples/guard/field/)
is one that uses the PLC4X connector, kept in its own Go module.

## Layout

| Package | Purpose |
|---|---|
| [alarm](alarm/) | State machine for one alarm (pure) |
| [evaluator](evaluator/) | Value → condition: limits, deadband, rates, bad quality, delays (pure) |
| [engine](engine/) | Runs the alarms, takes commands, sends events to sinks in order (pure) |
| [api](api/), [api/httpapi](api/httpapi/) | Service interface and its HTTPS/JSON adapter |
| [link](link/) | Links beeguard to remote honeycomb databases |
| [tagbridge](tagbridge/) | Sources, status tags and command tags in honeycomb |
| [journal](journal/), [filejournal](journal/filejournal/), [sqljournal](journal/sqljournal/) | Event journals |
| [sink/slogsink](sink/slogsink/) | Event log through `log/slog` |
| [config](config/) | JSON alarm definitions |
| [cmd/beeguard](cmd/beeguard/) | The server |
| [cmd/modbus_sim](cmd/modbus_sim/) | A simulated guard device over Modbus TCP |
| [examples/guard](examples/guard/) | A complete setup: simulator, field server, beeguard |

## Quick start

See [examples/guard](examples/guard/README.md): three terminals run the Modbus
simulator, the field server and beeguard, and the alarms appear within a minute.

## APIs

```bash
curl --cacert examples/guard/dev/cert.pem -H "Authorization: Bearer $BEEGUARD_TOKEN" \
     https://127.0.0.1:8444/v1/alarms/active
curl --cacert examples/guard/dev/cert.pem -H "Authorization: Bearer $BEEGUARD_TOKEN" \
     -d '{"command":"ack","user":"franklin"}' https://127.0.0.1:8444/v1/alarms/Guard1.TempHigh/commands
```

See [doc/api.md](doc/api.md) for the full API and [doc/tags.md](doc/tags.md)
for the status and command tags.

## Known limits

- Every change of a linked tag is received through the linked database's change
  feed, in order and with its device time, within milliseconds. A database
  without a change feed (an older honeycomb) is polled every `-poll` (1 s), and
  then changes shorter than one poll can be missed. Changes the field side
  itself never sees, between two device reads, cannot be recovered anywhere:
  use the connector's change-of-state mode or latch short pulses in the PLC.
- Alarm state is not persisted: after a restart an active condition is
  announced again as unacknowledged and shelving is cleared.

## Licensing

Dual licensed under GPLv3 or a commercial license, like honeycomb. Third-party
licenses, referenced standards and trademarks are listed in [NOTICE.md](NOTICE.md).
