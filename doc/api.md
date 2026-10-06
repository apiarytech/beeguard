# beeguard API

beeguard's API has two layers:

- [api](../api/api.go): the `api.Service` Go interface with plain request and
  response types. It knows nothing about transports, so it can be served over
  HTTP, gRPC or as a go-micro service handler without changing beeguard.
- [api/httpapi](../api/httpapi/httpapi.go): the interface served as JSON over
  HTTPS with the standard library. `cmd/beeguard` runs it on `-api-port`
  (8444 by default).

## The Service interface

```go
type Service interface {
    ListAlarms(ctx context.Context) ([]Alarm, error)
    ActiveAlarms(ctx context.Context) ([]Alarm, error)
    GetAlarm(ctx context.Context, id string) (Alarm, error)
    Command(ctx context.Context, req CommandRequest) (Alarm, error)
    QueryEvents(ctx context.Context, q EventQuery) ([]Event, error)
}
```

`api.New(engine, journal)` returns the implementation. Errors are `*api.Error`
with a `Code`, which a transport maps to its own status:

| Code | Meaning | HTTP |
|---|---|---|
| `not_found` | no such alarm | 404 |
| `invalid_argument` | malformed request, e.g. no user, unknown command | 400 |
| `failed_precondition` | not allowed in the alarm's state, e.g. ack when nothing to acknowledge | 409 |
| `unauthenticated` | missing or wrong token | 401 |
| `unavailable` | a dependency failed, e.g. the journal | 503 |
| `internal` | anything else | 500 |

### Embedding as a microservice

A go-micro (or gRPC) handler only translates between its own request types and
`api.Service`, for example:

```go
type AlarmHandler struct{ svc api.Service }

func (h *AlarmHandler) Ack(ctx context.Context, req *pb.AckRequest, rsp *pb.Alarm) error {
    a, err := h.svc.Command(ctx, api.CommandRequest{Alarm: req.Id, Command: api.CmdAck, User: req.User})
    if err != nil {
        return toMicroError(err) // map api.CodeOf(err) to the framework's error codes
    }
    *rsp = toProto(a)
    return nil
}
```

The engine, journal and tag bridge are set up exactly as in
[cmd/beeguard](../cmd/beeguard/main.go); only the transport changes.

## HTTPS/JSON

Every request needs `Authorization: Bearer <token>` (`-token`, or the variable
named by `-token-env`, `BEEGUARD_TOKEN` by default).

| Method and path | Body / query | Returns |
|---|---|---|
| `GET /v1/alarms` | | every alarm, in configuration order |
| `GET /v1/alarms/active` | | alarms in alarm or waiting for an acknowledgement, most urgent first |
| `GET /v1/alarms/{id}` | | one alarm |
| `POST /v1/alarms/{id}/commands` | `{"command": "ack", "user": "franklin"}` | the alarm after the command |
| `GET /v1/events` | `alarm`, `kind` (repeatable), `since`, `until` (RFC 3339), `limit`, `offset`, `oldest` (`true`: `limit` and `offset` count from the oldest match, to page through a range) | journal entries, oldest first |

Commands: `ack`, `reset`, `shelve` (with optional `shelve_minutes` and
`one_shot`), `unshelve`, `suppress`, `unsuppress`, `disable`, `enable`,
`reset-count`. `user` is required and is recorded in the journal.

`limit` defaults to 1000 and is capped at 10000. Event kinds are `ACTIVATED`,
`RTN`, `ACKNOWLEDGED`, `RESET`, `SHELVED`, `UNSHELVED`, `SUPPRESSED`,
`UNSUPPRESSED`, `DISABLED`, `ENABLED`, `CHATTER_STARTED`, `CHATTER_ENDED` and
`COUNT_RESET`.

An alarm looks like:

```json
{
  "id": "Guard1.TempHigh",
  "description": "Guard 1 brood temperature high",
  "state": "ACTIVE_UNACK", "state_code": 1, "isa": "B",
  "in_alarm": true, "acked": false, "condition": true,
  "shelved": false, "suppressed": false, "disabled": false, "chattering": false,
  "priority": 2, "severity": 700, "count": 1,
  "in_alarm_time": "2026-10-01T12:54:17.627-10:00"
}
```

An error looks like `{"code": "failed_precondition", "message": "Guard1.TempHigh: alarm: nothing to acknowledge"}`.
