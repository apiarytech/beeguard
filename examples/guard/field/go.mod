module github.com/apiarytech/beeguard/examples/guard/field

go 1.27.1

// Develop against the sibling checkouts.
replace (
	github.com/apiarytech/beeguard => ../../..
	github.com/apiarytech/honeycomb => ../../../../honeycomb
	github.com/apiarytech/honeycomb/connectors/plc4x => ../../../../honeycomb/connectors/plc4x
)

require (
	github.com/apiarytech/beeguard v0.0.0-00010101000000-000000000000
	github.com/apiarytech/honeycomb v0.0.0
	github.com/apiarytech/honeycomb/connectors/plc4x v0.0.0-00010101000000-000000000000
	github.com/rs/zerolog v1.35.1
)

require (
	github.com/apache/plc4x/plc4go v0.0.0-20260930074747-b878affa2d7b // indirect
	github.com/apiarytech/royaljelly v0.1.0-beta1 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
