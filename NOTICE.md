# Notices

beeguard
Copyright (C) 2026 Franklin D. Amador

beeguard is dual-licensed under the GNU General Public License version 3 or a
commercial license; see [LICENSE.md](LICENSE.md) and [gpl-3.0.md](gpl-3.0.md).

## Third-party software

Each component below remains under its own license, and its copyright notice
and license text must accompany any distribution in source or binary form. The
full license texts are reproduced in the THIRD_PARTY_LICENSES files, generated
by `scripts/gen-third-party-licenses.sh` (see [doc/development.md](doc/development.md#third-party-licenses)).

### beeguard (default build)

Full texts: [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt).

| Component | Version | License |
|---|---|---|
| github.com/apiarytech/honeycomb | development checkout | GPLv3 or commercial (same author) |
| github.com/apiarytech/royaljelly | v0.1.0-beta1 | GPLv2 or later, or commercial (same author) |

Everything else in the default build comes from the Go standard library.

### beeguard built with `-tags sqlite`

Full texts: [THIRD_PARTY_LICENSES-sqlite.txt](THIRD_PARTY_LICENSES-sqlite.txt).
In addition to the default build:

| Component | Version | License |
|---|---|---|
| modernc.org/sqlite | v1.60.1 | BSD 3-Clause (embeds SQLite, public domain) |
| modernc.org/libc, mathutil, memory | v1.77.1, v1.7.1, v1.12.1 | BSD 3-Clause |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD 3-Clause |
| golang.org/x/sys | v0.48.0 | BSD 3-Clause |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/mattn/go-isatty | v0.0.24 | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | MIT |

### Field server example (`examples/guard/field`)

A separate program and Go module. Full texts:
[examples/guard/field/THIRD_PARTY_LICENSES.txt](examples/guard/field/THIRD_PARTY_LICENSES.txt).
In addition to honeycomb, its plc4x connector and royaljelly:

| Component | Version | License |
|---|---|---|
| github.com/apache/plc4x/plc4go | v0.0.0-20260930074747-b878affa2d7b | Apache License 2.0 |
| github.com/rs/zerolog | v1.35.1 | MIT |
| github.com/fatih/color | v1.19.0 | MIT |
| github.com/mattn/go-colorable, go-isatty | v0.1.15, v0.0.24 | MIT |
| golang.org/x/sys, x/text | v0.48.0, v0.42.0 | BSD 3-Clause |

This product includes software developed at The Apache Software Foundation
(https://www.apache.org/): Apache PLC4X (plc4go), licensed under the Apache
License, Version 2.0.

## Standards and other references

beeguard implements concepts described in published industry standards and
guidance. No text, tables or figures from these documents are reproduced;
they are cited by name only.

- ANSI/ISA-18.2, *Management of Alarm Systems for the Process Industries*, and
  the technical reports ISA-TR18.2.x, International Society of Automation.
- IEC 62682, *Management of alarm systems for the process industries*,
  International Electrotechnical Commission.
- EEMUA Publication 191, *Alarm systems: a guide to design, management and
  procurement*, Engineering Equipment and Materials Users Association.
- IEC 61131-3, *Programmable controllers – Programming languages*.

See [doc/decisions.md](doc/decisions.md#sources-and-originality) for how these
and publicly available vendor documentation were used.

## Trademarks

ISA is a trademark of the International Society of Automation. IEC is a
trademark of the International Electrotechnical Commission. EEMUA is a
trademark of the Engineering Equipment and Materials Users Association.
Rockwell Automation, Logix, Studio 5000 and FactoryTalk are trademarks of
Rockwell Automation, Inc. Modbus is a trademark of Schneider Electric USA,
Inc. (administered by the Modbus Organization). Apache and PLC4X are
trademarks of The Apache Software Foundation. CODESYS is a trademark of
CODESYS GmbH. All other trademarks are the property of their respective
owners. Their use in this project is for identification and reference only
and does not imply endorsement by, or affiliation with, their owners.
beeguard is not affiliated with or endorsed by any of the organizations
named above.
