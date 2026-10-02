# beeguard documentation

| Document | Contents |
|---|---|
| [architecture.md](architecture.md) | High-level structure: components, packages, data flow, concurrency |
| [decisions.md](decisions.md) | Design decisions made so far, why, and what they cost |
| [alarm-model.md](alarm-model.md) | The ISA-18.2 alarm states, transitions, commands and events |
| [conditions.md](conditions.md) | How values become alarm conditions: limits, deadband, rates, delays, bad quality |
| [api.md](api.md) | The beeguard API: the Go service interface and its HTTPS/JSON form |
| [tags.md](tags.md) | The honeycomb status and command tags an HMI uses |
| [configuration.md](configuration.md) | Links, alarm definitions and flags |
| [development.md](development.md) | Building, testing, the simulator, and working without network access |

The function blocks in [../reference/](../reference/) are the IEC 61131-3
Structured Text specification the Go alarm model was ported from.
