# Design decisions

A log of the decisions behind beeguard's structure, in the order they were
made. Each entry says what was decided, why, and what it costs. Open items are
at the end.

Last updated: 2026-10-01.

---

## D-1. Build our own alarm model in Go

**Decision.** Implement the ISA-18.2 / IEC 62682 alarm state model ourselves.

**Why.** No Go library implements ISA-18.2 alarm management. The closest Go
projects are Prometheus Alertmanager (silences ≈ shelving, inhibition ≈
suppression, but no acknowledge/return-to-normal state machine and built for
IT alerting) and `gopcua` (an OPC UA client with limited Alarms & Conditions
support). Open ISA-18.2 implementations exist only in other languages.

**Cost.** We own the correctness of the state model, so it is covered by
unit tests for every combination of acknowledge-required and latched, and for
each hidden state.

## D-2. charmbracelet/log is an output, not the core

> Updated by D-28: the console output now uses the standard library's `log/slog`.

**Decision.** Use charmbracelet/log only to print events (`sink/charm`).

**Why.** A logger is stateless: each line is written and forgotten. An alarm
server must hold per-alarm state over time, enforce which commands are allowed
in which state, and expire shelves. Logger levels are verbosity filters, not
alarm priorities.

## D-3. The Structured Text block is the specification

**Decision.** The `BG_ALM_DIG` function block ([reference/digital_alarm.st](../reference/digital_alarm.st))
is the reference behaviour, and `alarm.State` uses its `VAL_STATE` numbering
(0 NORMAL … 8 OUT_OF_SERVICE).

**Why.** The original v2 block (now in [archive/](../archive/)) had defects
that made latching and shelving not work. Rewriting it against ISA-18.2 first
gave one agreed behaviour for both the PLC and the server. Decisions made in
the Structured Text that carry over:

- Operator requests (`CMD_OP_*`) are one-shot and cleared by the block; logic
  requests (`CMD_PG_*`) act on a rising edge, so logic may hold them.
- Shelving is operator-only, so there is no `CMD_PG_SHELVE`.
- Enable, unsuppress and unshelve win over their opposites in the same scan.
- The analog block (`BG_ALM_ANA`) runs one digital block per condition
  instead of duplicating the state machine.

## D-4. Pure core packages with explicit time

**Decision.** `alarm`, `evaluator` and `engine` have no I/O, no goroutines
(apart from `engine.Run`) and no honeycomb dependency. Every method takes the
current time.

**Why.** Deterministic tests: a test can step time by milliseconds or hours
without sleeping. The alarm logic can be reused with another tag source.

**Cost.** Timers do not fire by themselves; `Tick` must be called
periodically (`engine.Run` does this every 100 ms by default).

## D-5. Condition filtering is separate from the state machine

**Decision.** `evaluator` turns values into a condition (limits, deadband,
rates, on/off delays, bad-quality hold); `alarm` only applies the condition
to the state model.

**Why.** It follows the ISA-TR18.2.3 filtering order (deadband, then on-delay,
then off-delay) in one place, and one state machine serves every kind of
condition. In the Structured Text the on/off delays sit inside `BG_ALM_DIG`;
in Go they moved to the evaluator.

## D-6. Hidden alarms are held at NORMAL and re-annunciated

**Decision.** While an alarm is shelved, suppressed or out of service its state
is held at NORMAL. When it is shown again, an active condition re-annunciates
it as ACTIVE_UNACK and counts as a new activation.

**Why.** An operator must never miss an alarm because it was hidden when it
started. This is the conservative reading of the ISA-18.2 state diagram.

**Cost.** An acknowledgement given while an alarm is shelved does not carry
over; some commercial controllers keep it. Latching is also lost while hidden.

## D-7. Shelving is operator-only and always time-limited

**Decision.** `MaxShelve` of zero means an alarm cannot be shelved. Requested
durations are capped at `MaxShelve`; zero or negative means the maximum.
Shelving again restarts the shelve. One-shot shelving ends when the condition
returns to normal and is only allowed on an active alarm.

**Why.** ISA-18.2 requires shelving to be controlled and time-limited, so it
cannot become a permanent way to hide alarms.

## D-8. Displayed-state precedence

**Decision.** The displayed state is OUT_OF_SERVICE, then SUPPRESSED, then
SHELVED, then the alarm state. Taking an alarm out of service ends its shelve.

**Why.** The most deliberate removal wins, and maintenance (out of service)
supersedes an operator's temporary shelve.

## D-9. Bad quality holds the condition

**Decision.** A sample with bad quality leaves the condition as it was, and
rate measurement restarts after bad data. A separate `bad-quality` condition
kind alarms on the quality itself. honeycomb `Uncertain` quality counts as
good; `Bad` and `Unknown` count as bad.

**Why.** Bad data must neither raise nor clear an alarm, and a jump between
bad and good data must not look like a rate of change.

## D-10. Severity 1..1000 maps to four priorities

**Decision.** Severity ≤ 250 is Low, ≤ 500 Medium, ≤ 750 High, above that
Urgent. Severity defaults to 500.

**Why.** A wide severity range is common in industrial alarm systems, so
existing severity values can be reused, while operators see the small number
of priorities ISA-18.2 recommends.

## D-11. Chattering uses a sliding window

**Decision.** An alarm is chattering while its last `ChatterCount`
activations all fall within `ChatterWindow`. Activations are counted also while
the alarm is hidden.

**Why.** A sliding window clears as soon as the alarm calms down, unlike the
fixed window the Structured Text uses. Counting hidden activations shows when
a shelved alarm is still chattering.

## D-12. Events are delivered in order through a queue

**Decision.** The engine appends events to a queue under its state lock; the
producing goroutine then takes a separate publish lock and drains the queue.
The publish lock is never taken while holding the state lock. Sinks may read
the engine but must not issue commands.

**Why.** The first design handed the publish lock over while holding the state
lock. The end-to-end test found a deadlock: a sink reading `Status` waited for
the state lock, while another goroutine held it waiting to publish. A
regression test now runs concurrent processing with a sink that reads the
engine.

## D-13. Two timestamps per event

**Decision.** `Event.Time` is when beeguard recorded the event; `SourceTime`
is the device timestamp of the value that caused it.

**Why.** Sequence-of-events analysis needs the device time, while audit and
operator response times need the server time. honeycomb was extended to carry
a per-tag `Timestamp` for this.

## D-14. The journal has its own table and stores Unix nanoseconds

**Decision.** `sqljournal` creates `beeguard_events` with its own idempotent
DDL per database, and stores times as Unix nanoseconds in UTC.

**Why.** honeycomb's migration runner records versions in one fixed table, so
beeguard migrations would collide with honeycomb's. Integer times sort and
compare the same in every database and avoid driver time-zone differences.

**Cost.** No schema versioning yet. Only SQLite is tested.

## D-15. Sink order: journal, console, bridge

> Still applies; the console sink is now `slogsink` (D-28).

**Decision.** `cmd/beeguard` registers the journal first.

**Why.** The record is written before anything else reacts to the event.

## D-16. The tag bridge runs in-process and treats notifications as signals

> Updated by D-29 and D-30: beeguard now links to remote databases and polls the linked tags; local tags are still subscribed to as described here.

**Decision.** beeguard runs in the same process as its honeycomb
`TagDatabase`. On each subscription notification the bridge re-reads the tag
with `GetTag` instead of using the delivered value.

**Why.** honeycomb's network API has no push channel, so in-process is the only
way to get change events. Receiving honeycomb's `Tag` by value copies its mutex,
which `go vet` rejects; since the channel delivers only the latest state anyway,
re-reading loses nothing.

**Cost.** honeycomb keeps only the latest update per subscriber, so a pulse
shorter than the bridge's reaction time can be missed. Short pulses should be
latched or debounced in the device.

## D-17. Status and command tags are UDTs

**Decision.** Each alarm gets an `AlarmStatus` UDT tag that the bridge rewrites
whole, and an `AlarmCommand` UDT tag whose BOOL fields the HMI sets and the
bridge clears one at a time. The outcome is written to `Command.Result`.

**Why.** It is the PLC pattern operators and HMI developers already use (the
`CMD_OP_*` inputs of the function block). Clearing fields individually through
honeycomb means a command set while another runs is not lost. Commands run in
field order, so Enable/Unsuppress/Unshelve win and Ack runs before Reset.

## D-18. Tag names replace `.` with `_`

**Decision.** Status tag = prefix (`ALM_`) + alarm ID with every character
other than letters, digits and `_` replaced by `_`; command tag adds `_CMD`.

**Why.** honeycomb reads the first `.` in a name as a UDT field separator, so
`ALM_Guard1.TempHigh.State` would not resolve.

## D-19. Configuration is strict JSON in the plc4x style

**Decision.** Alarm definitions are JSON like the honeycomb plc4x connector's
configuration, with Go duration strings. Unknown keys are rejected.
`severity` defaults to 500 and `ackRequired` to true.

**Why.** One configuration style across the project, no extra dependency, and a
misspelled key fails at startup instead of silently using a default.

## D-20. A Modbus simulator instead of PLC4X's simulated driver

> Still applies; the simulator now feeds the field server in `examples/guard/field` (D-29).

**Decision.** beeguard ships a small Modbus TCP server (`internal/modbus_sim`)
and a simulated guard device that cycles through every example alarm.

**Why.** plc4go's simulated driver is in an `internal` package and cannot be
imported. A real Modbus server also tests the real PLC4X driver path, which
found the tag value-type bug during development.

## D-21. Dependencies

> Updated by D-28, D-29 and D-31: the default build now has no third-party modules other than honeycomb and royaljelly.

**Decision.**

- Console logging uses `charm.land/log/v2` with `charm.land/lipgloss/v2`, the
  versions bdTUI already uses.
- honeycomb and its plc4x connector come from the sibling checkout through
  `replace` directives in `go.mod`.
- `go.sum` was seeded from the verified `go.sum` files of bdTUI and honeycomb.

**Why.** Consistency with the other apiarytech projects, and development
against unreleased honeycomb changes. The development environment could not
reach the Go checksum database, so modules came from the local cache.

**Cost.** Run `go mod verify` with network access. The `replace` directives
must be removed or pinned before beeguard is published.

## D-22. PLC4X logging is quieted through zerolog

> Moved with PLC4X to `examples/guard/field` (D-29).

**Decision.** `-plc4x-log` (default `error`) sets zerolog's global level.

**Why.** plc4go writes trace-level JSON to stderr by default, and honeycomb's
`NewDriverManager` does not accept a logger. Connection errors still reach
beeguard through the connector's error handler.

## D-23. Alarm state is not persisted

**Decision.** Alarm state lives in memory only.

**Why.** It is the safe default: after a restart an active condition is
re-annunciated as unacknowledged and shelving is cleared, so nothing stays
hidden by accident. Persisting state (for example in honeycomb `Retain` tags)
can be added later.

## D-24. Tags are created from the I/O addresses

> Moved with PLC4X to `examples/guard/field` (D-29).

**Decision.** `cmd/beeguard` creates a tag for every PLC4X binding that has
none, typed from the data type at the end of the address
(`holding-register:1:REAL` → `REAL`). Array addresses are rejected.

**Why.** The connector requires each bound tag to exist with a value of the
right Go type; inferring it keeps the I/O file the single source.

## D-25. Naming

**Decision.** The example setup is `examples/guard`, the simulated device is
`modbussim.Guard` with tags `Guard1.*`, and the simulator lives in
`cmd/modbus_sim` and `internal/modbus_sim`. The Go package clause stays
`modbussim`.

**Why.** The example represents an alarm and event server (guard) setup. Go
convention and linters reject underscores in package names, so only the
directories use `modbus_sim`.

## D-26. Licensing follows honeycomb

**Decision.** beeguard is dual-licensed GPLv3 / commercial, with the same file
header as honeycomb.

**Why.** beeguard links honeycomb, which is under the same terms. Every linked
dependency is compatible: royaljelly is GPLv2-or-later or commercial, plc4go is
Apache-2.0, and the rest are MIT or BSD. They are listed in
[NOTICE.md](../NOTICE.md).

## D-27. Own names, independent of any vendor's implementation

**Decision.** The function blocks are `BG_ALM_DIG` and `BG_ALM_ANA`, with
parameters named in the project's own `IN_` / `CFG_` / `CMD_OP_` / `CMD_PG_` /
`STS_` / `VAL_` / `TS_` scheme, the one the analog-input block already used.
Comments and documentation describe behaviour in our own words and refer to
standards and vendor documents only by name. The polarity input is
`CFG_INVERT` (TRUE: alarm while the signal is FALSE), which says what it does.

**Why.** The earlier names (`AOI_CM_ALMD`, `AOI_CM_ALMA`, `OPERACK`,
`MINDURATIONPRE`, …) followed one vendor's instruction set closely enough to
suggest a copy or an affiliation. Implementing the ISA-18.2 model is open to
anyone; reproducing a vendor's interface is not necessary for it. See
[Sources and originality](#sources-and-originality).

**Cost.** PLC projects that used the v3.0 names must be updated (version 3.1 of
the digital block, 1.1 of the analog block, 2.3 of the archived analog input).
The original v2 files keep their original names as a historical record.

## D-28. Console logging uses the standard library's log/slog

**Decision.** `sink/slogsink` replaces `sink/charm`; `cmd/beeguard` logs through
`log/slog` with `-log-format text|json` and `-log-level`.

**Why.** charmbracelet/log and lipgloss brought 16 of the 30 third-party
modules, only for coloured console output. `log/slog` gives the same levels and
structured fields, and JSON for log collectors, with no dependency.

**Cost.** No colours in the terminal.

## D-29. beeguard links to honeycomb databases instead of reading devices

**Decision.** beeguard reads its source tags from other honeycomb databases
through honeycomb's HTTPS tag API (package `link`), and serves its own tags and
API over HTTPS. Device I/O is the job of a field-side program; the PLC4X one is
in `examples/guard/field`, a separate Go module.

**Why.** It is the honeycomb model: databases link to each other. beeguard can
watch several field databases, it no longer depends on PLC4X (seven modules),
and plc4x is used by no other apiarytech program, so it stays in honeycomb's
connector and in the example.

**Cost.** Values are polled (D-30), and the deployment has two processes.

## D-30. Linked tags are polled with one request per tag

> Updated by D-34: linked databases with a change feed are no longer polled; polling remains the fallback.

**Decision.** honeycomb gained `TagDatabase.ReadTag`, which returns value,
quality and timestamp together; for a remote alias it makes one HTTPS request,
and the network client now reads the timestamp the server already sends. The
tag bridge polls linked tags with it every `-poll` (1 s). A failed read is a
bad-quality sample, reported once when the failure starts.

**Why.** honeycomb cannot notify on remote aliases. One request per tag keeps
polling affordable, and carrying the timestamp keeps sequence-of-events times
(D-13) across the link.

**Cost.** Reaction time up to one poll; pulses shorter than a poll can be
missed; each poll is one request per linked tag. A push or long-poll
subscription in honeycomb would remove all three.

## D-31. The journal is a JSON Lines file by default; SQLite is optional

**Decision.** `journal/filejournal` appends one JSON object per event and syncs
after each batch. `-journal` picks the journal by extension: `.jsonl`,
`.db`/`.sqlite` (only in builds with `-tags sqlite`), or empty for memory.

**Why.** No dependency, human-readable, easy to ship to a log collector. SQLite
adds nine modules, so it is opt-in at build time and listed separately in
`THIRD_PARTY_LICENSES-sqlite.txt`.

**Cost.** `Query` reads the whole file, so long histories need rotation or the
SQL journal.

## D-32. A transport-neutral API, with HTTPS/JSON as the first transport

**Decision.** `api.Service` (alarms, commands, journal queries) uses plain
request/response types and errors with a `Code`. `api/httpapi` serves it over
HTTPS/JSON with the standard library. No microservice framework is a
dependency.

**Why.** beeguard is to be embedded later as a go-micro service in an
enterprise solution. A go-micro (or gRPC) handler then only maps its own types
and error codes to `api.Service`; the alarm logic does not change, and the
framework's dependencies are taken on only when that project needs them.

## D-33. Secure defaults for links and APIs

**Decision.**

- Links and both APIs are HTTPS only, with TLS 1.2 as the minimum, and
  certificates are verified; a self-signed certificate is trusted through a CA
  file, never by switching verification off.
- Bearer tokens are compared in constant time, can be read from environment
  variables (`tokenEnv`, `-token-env`) so they stay out of files and process
  lists, and the servers refuse to start without one.
- `-dev-cert` creates a self-signed certificate only when asked, with a warning
  in the log; `examples/guard/.gitignore` keeps the private key out of version
  control.
- Every API command must name a user, which goes into the journal.

**Why.** Alarm commands change what operators see; they must be authenticated
and attributable, and secrets must not leak through configuration files.

**Cost.** One shared token per API for now: no per-user identities or roles.

## D-34. Linked tags come from the field database's change feed

**Decision.** honeycomb records every change of a top-level tag in an
in-memory ring buffer (10,000 by default) with a sequence number and an epoch,
and serves it as a long-poll (`POST /changes`). The tag bridge follows each
linked database's feed: every change of the linked tags is processed in order
with its device timestamp. It reads current values (one `ReadTags` batch
request per database, or `ReadTag` per tag on a server without it) when it
starts, after a gap (buffer overrun or restart, which is logged) and after a
failed request. A server without a feed answers 404 and is polled instead.

**Why.** Polling samples values, so changes between polls were lost and the
reaction time was up to one poll. A buffer inside beeguard cannot fix that: it
only sees what it reads. The buffer has to be where changes happen, on the
field side. With the feed every change arrives within milliseconds, and the
per-tag polling load and request logging disappear.

**Cost.** Memory for the buffer on the field side (about 1–2 MB at 10,000
changes). A reader that falls further behind than the buffer must resynchronise
and may miss changes in the gap. Changes the field side never sees, between two
reads of a device, are still lost; that is fixed at the device (change-of-state
mode, PLC latching). Since the honeycomb update, rewrites of an unchanged value
are not recorded, and array and UDT values are deep-copied when recorded.

## D-35. Delays and rates are evaluated in device time

**Decision.** With `engine.WithSourceTime(maxSkew)` (`-source-time-skew`, 5 s)
a sample is evaluated at its device timestamp when that is within `maxSkew` of
the server clock, and at the server clock otherwise. Evaluation time never goes
backwards for an alarm.

**Why.** Changes from a feed can arrive together. Timed by arrival, a condition
that lasted 3 s in the field could still wait out a 2 s on-delay, and a burst
would distort rates of change. The skew limit keeps a wrong device clock from
distorting alarms.

## D-36. The engine is driven by deadlines, not a fixed tick

**Decision.** Evaluators and alarms report their next deadline (delay expiry,
rate measurement, shelve expiry, end of chattering). `engine.Run` sleeps until
the earliest one and is woken when a sample or command may have created an
earlier one; `-tick` (now 1 s) is only a safety net.

**Why.** With a 100 ms tick a delay could end up to 100 ms late, and the engine
woke ten times a second with nothing to do. Deadlines make delays end on time
and an idle server idle.

## D-37. The tag bridge subscribes to command tags before sources

**Decision.** At startup the bridge subscribes to the command tags and runs any
command already set before it starts reading sources. Status tags are written
under a lock with the alarm's current status at the time of writing.

**Why.** A stress run found that an alarm could activate, and an HMI could set
`Ack`, before the command tag was subscribed, so the command was never seen;
and a status written from an older snapshot could overwrite a newer one.

---

## Open items

| Item | Notes |
|---|---|
| Legal review | Before commercial release: an IP attorney's review, including a patent freedom-to-operate search, and a contributor license agreement (CLA) so dual licensing stays possible. |
| Alarm state persistence | See D-23. |
| Journal schema versioning and rotation | See D-14 and D-31. |
| honeycomb changes not committed | `ReadTag`, `ReadTags`, `TagReader` and the change feed are in the honeycomb working tree; beeguard builds against it through the `replace` directive. |
| honeycomb: `DT` JSON encoding | royaljelly's `DT` has no JSON encoding, so time fields of UDT tags read over HTTPS come through as `{}`. |
| honeycomb: request logging | The tag server logs every GET; less of an issue now that linked tags use the feed. |
| honeycomb: subscription value type | `chan Tag` carries a mutex; a lock-free snapshot type would remove the workaround in D-16. |
| honeycomb: driver manager options | `NewDriverManager` should accept plc4go options such as a logger (D-22). |
| honeycomb: migration table name | A configurable migrations table would let beeguard reuse honeycomb's runner (D-14). |
| honeycomb: existing `go vet` findings | Lock copies in honeycomb's own tests and `TagDatabase.go`; `network_server.go` needs gofmt. Not from beeguard's changes. |
| API identities | Per-user authentication and roles (e.g. OIDC/JWT) instead of one shared token (D-33). |
| go-micro service | Wrap `api.Service` when the enterprise project starts (D-32). |
| Race detector | Not run: the development machine has no C compiler for cgo. |
| Other databases | `sqljournal` DDL for PostgreSQL, MySQL and SQL Server is untested. |

---

## Sources and originality

A record of what beeguard drew on and what is its own. It is kept up to date
as part of the project, so the origin of each part can be shown later.

### What was used, and how

| Source | Used for | How |
|---|---|---|
| ANSI/ISA-18.2 and IEC 62682 | The alarm states A–H, acknowledge, latching, shelving, suppression by design, out of service | Concepts implemented from the published model. No text, tables or figures copied. |
| ISA-TR18.2.3 and EEMUA 191 | Filtering order (deadband, on-delay, off-delay); typical starting deadbands and delays; chattering as a nuisance-alarm indicator | The numeric starting values are cited as facts with their source. No text or tables copied. |
| Rockwell Automation public instruction reference for its digital and analog alarm instructions (read online, Studio 5000 v38 documentation) | Checking that the planned behaviour covers what industrial users expect from a controller-based alarm: which requests exist, when they take effect, which time stamps are kept | Read for reference only. No code, text or figures copied. beeguard's names, structure and several behaviours differ deliberately (D-6, D-11, D-27, and configuration errors refuse to start instead of setting fault bits). |
| The author's own v2 function blocks (`archive/`, `reference/analog_v2.1.st`) | Starting point for the Structured Text and the analog-input block | Owned by the author. Reviewed, corrected and rewritten as v3. |
| Open-source components (see [NOTICE.md](../NOTICE.md)) | Tag database; SQLite in the optional SQLite build; device drivers in the field example | Used as libraries under their licenses. No code copied into beeguard. |
| Public projects surveyed for the design (Prometheus Alertmanager, gopcua, PyAutomation, EVA ICS) | Confirming no Go ISA-18.2 library exists; general ideas such as silences and inhibition | Read for comparison only. No code used. |

### What is original to beeguard

- The Go code in every package, written from scratch for this project.
- The architecture: pure state and condition packages driven by explicit time,
  an engine that delivers events to sinks in order, and a bridge that turns a
  tag database into the source of alarms and the place where operators send
  requests.
- The split of condition filtering (`evaluator`) from the state model
  (`alarm`), and one alarm per condition with several alarms sharing a source.
- Behaviour choices: re-announcing an alarm when it leaves a hidden state
  instead of keeping an acknowledgement given while hidden; a sliding chatter
  window; bad-quality samples holding the condition and restarting rate
  measurement; refusing invalid configuration at startup.
- The status and command UDTs and their field order, the JSON alarm
  configuration, the journal layout with device and server time stamps, and the
  Modbus simulator.
- The Structured Text blocks `BG_ALM_DIG` and `BG_ALM_ANA` in their v3.1 / 1.1
  form: names, comments and structure.

### Rules for future work

- Describe behaviour in our own words; cite standards and vendor documents by
  name rather than quoting them.
- Do not copy code, text, tables or figures from standards, vendor manuals or
  other products. Facts such as typical numeric values may be cited with their
  source.
- Do not adopt a vendor's instruction or parameter names. Use the project's
  naming scheme (D-27).
- Mention vendor products only to identify them, for example for
  compatibility, and keep the trademark notice in [NOTICE.md](../NOTICE.md)
  current.
- Record any new external source in the table above.
