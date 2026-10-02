# Status and command tags

Package [tagbridge](../tagbridge/) connects the engine to a honeycomb
`TagDatabase`. For each alarm it creates two UDT tags. For an alarm with ID
`Guard1.TempHigh` and the default prefix `ALM_`:

| Tag | UDT | Written by |
|---|---|---|
| `ALM_Guard1_TempHigh` | `BeeguardAlarmStatus` | the bridge, on every event of the alarm |
| `ALM_Guard1_TempHigh_CMD` | `BeeguardAlarmCommand` | the HMI or PLC logic; the bridge clears it |

Characters other than letters, digits and `_` in the alarm ID become `_`,
because honeycomb reads the first `.` of a name as a UDT field separator.

## AlarmStatus

| Field | Type | Meaning |
|---|---|---|
| `State` | DINT | 0 NORMAL … 8 OUT_OF_SERVICE (see [alarm-model.md](alarm-model.md)) |
| `InAlarm` | BOOL | active or latched |
| `Acked` | BOOL | no acknowledgement pending |
| `InAlarmUnack` | BOOL | in alarm and unacknowledged |
| `Unacked` | BOOL | any acknowledgement pending, including RTN_UNACK (horn) |
| `Condition` | BOOL | the filtered condition, also while hidden |
| `Shelved`, `Suppressed`, `Disabled` | BOOL | hidden modes |
| `Chattering` | BOOL | chatter detection |
| `Priority` | DINT | 1 urgent … 4 low |
| `Severity` | DINT | 1..1000 |
| `AlarmCount` | DINT | activations since the last count reset |
| `InAlarmTime`, `AckTime`, `RTNTime`, `ShelveExpiry` | DT | event times |

## AlarmCommand

Set a BOOL field to TRUE; the bridge runs the command, sets the field back to
FALSE and writes the outcome to `Result`.

| Field | Type | Meaning |
|---|---|---|
| `Disable`, `Enable` | BOOL | out of service / back in service |
| `Suppress`, `Unsuppress` | BOOL | suppression by design |
| `Shelve`, `Unshelve` | BOOL | shelving; uses `ShelveMinutes` and `OneShot` |
| `Ack` | BOOL | acknowledge |
| `Reset` | BOOL | reset a latched alarm |
| `ResetCount` | BOOL | set `AlarmCount` to 0 |
| `ShelveMinutes` | DINT | duration of the next shelve; 0 = the alarm's maximum |
| `OneShot` | BOOL | the next shelve ends when the condition returns to normal |
| `User` | STRING | operator recorded in the journal; empty = the `-user` default |
| `Result` | STRING | `OK`, or why the last command failed |

When several fields are set at once they run in the order above, so Enable,
Unsuppress and Unshelve win over their opposites and Ack runs before Reset.

Examples, through honeycomb in Go:

```go
db.SetTagValue("ALM_Guard1_TempHigh_CMD.User", plc.STRING("franklin"))
db.SetTagValue("ALM_Guard1_TempHigh_CMD.Ack", plc.BOOL(true))

// Shelve for 30 minutes.
db.SetTagValue("ALM_Guard1_TempHigh_CMD.ShelveMinutes", plc.DINT(30))
db.SetTagValue("ALM_Guard1_TempHigh_CMD.Shelve", plc.BOOL(true))
```

Over honeycomb's HTTPS tag API, which beeguard serves on `-tags-port` (8443),
read `GET /tags/ALM_Guard1_TempHigh` and write
`PUT /tags/ALM_Guard1_TempHigh_CMD.Ack`. Another honeycomb database can also
link to these tags as remote aliases. Reading the status tag over HTTPS is
tested; the `DT` time fields currently come through as `{}` because royaljelly's
`DT` type has no JSON encoding, so read times through the beeguard API
([api.md](api.md)) instead.

For new integrations prefer the beeguard API: it has typed errors, requires a
user for every command, and also serves the journal.

## Source tags

A source tag linked from another honeycomb database (see
[configuration.md](configuration.md#links--links)) is a remote alias. The
bridge follows that database's **change feed**: one long-poll request (renewed
every `-feed-wait`, 25 s) returns every change of the linked tags since the last
one, in order, with value, quality and device timestamp. Every change reaches
the alarms, including pulses far shorter than any poll interval, within
milliseconds of the field database recording it.

When following starts, and again after a gap (the field database's buffer
overran or it restarted) or a failed request, the bridge first reads the
current values, with one batch request (`ReadTags`) or, on a server without
the batch read, one `ReadTag` per tag; then it continues with the feed. A gap is logged,
because changes in it may be missing.

A linked database without a change feed (an older honeycomb) is polled with
`ReadTag` every `-poll` (1 s) instead; then a change shorter than one poll can be
missed.

A local source tag is subscribed to. honeycomb keeps only the latest update per
subscriber, so a pulse shorter than the bridge's reaction time can be missed
there; use a linked database to get every change.

A source that cannot be read, for example because the remote database is
unreachable, is passed on as bad quality: alarms on it hold their condition and
`bad-quality` alarms raise. The bridge reports the failure once, when it
starts.

Changes the field side never sees, between two reads of the device, cannot be
recovered by any of these: use the connector's change-of-state mode or latch
short pulses in the PLC.
