# Conditions

Package [evaluator](../evaluator/) turns a stream of samples from one source
into an alarm condition. Each alarm has its own evaluator; several alarms can
share a source (for example a high and a high-high alarm on one temperature).

## Kinds

| Kind | Active when | Clears when |
|---|---|---|
| `digital` | value ≠ 0 (value = 0 with `invert`) | the opposite |
| `high` | value > `limit` | value < `limit − deadband` |
| `low` | value < `limit` | value > `limit + deadband` |
| `rate-of-rise` | rise over one `period` > `limit` units/second | the rate falls back to ≤ `limit` |
| `rate-of-fall` | fall over one `period` > `limit` units/second | the rate falls back to ≤ `limit` |
| `bad-quality` | the sample's quality is bad | quality is good |

BOOL tags are 0 or 1. Integer and real tags are converted to `float64`.

## Filtering order

Following ISA-TR18.2.3:

1. **Limit with deadband**: inside the deadband the condition keeps its last value.
2. **On-delay**: the condition must stay active for `onDelay` before it becomes active.
3. **Off-delay**: the condition must stay clear for `offDelay` before it clears.

A delay restarts if the condition flips back before it expires.

Commonly cited starting values (ISA-TR18.2.3 / EEMUA 191), to tune per loop:

| PV type | Deadband (% of span) | On/off delay |
|---|---|---|
| Flow | 5 % | 15 s |
| Level | 5 % | 60 s |
| Pressure | 2 % | 15 s |
| Temperature | 1 % | 60 s |

## Rates of change

The rate is measured once per `period`: (value now − value one period ago) /
elapsed seconds. `Tick` re-measures with the last value when no new sample
arrives, so a rate falls back to zero when the value stops changing. A rate
alarm usually needs no on-delay, because the period already filters it.

## Bad quality

honeycomb `Good` and `Uncertain` count as good; `Bad` and `Unknown` (never
written) count as bad.

- For every kind except `bad-quality`, a bad sample leaves the condition as it
  was, so bad data can neither raise nor clear an alarm.
- Rate measurement restarts after bad data, so the jump between bad and good
  values is not measured as a rate.
- Use a `bad-quality` alarm on the same source to alarm on the failure itself;
  give it an on-delay so tags that are still `Unknown` at startup do not alarm.
