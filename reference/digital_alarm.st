FUNCTION_BLOCK BG_ALM_DIG
(*==========================================================================
BG_ALM_DIG - beeguard digital alarm.
Implements the alarm states of ISA-18.2 / IEC 62682 for one BOOL signal.

NAMING
	IN_   process inputs            CMD_OP_  operator requests (one-shot)
	CFG_  configuration             CMD_PG_  logic requests (rising edge)
	STS_  BOOL status outputs       VAL_     numeric outputs
	TS_   event time stamps (DT)

VAL_STATE
	0 NORMAL          ISA A  signal normal, nothing to acknowledge
	1 ACTIVE_UNACK    ISA B  in alarm, not yet acknowledged
	2 ACTIVE_ACK      ISA C  in alarm, acknowledged
	3 RTN_UNACK       ISA D  back to normal before anyone acknowledged it
	4 LATCHED_UNACK   ISA E  latched, back to normal, not yet acknowledged
	5 LATCHED_ACK     ISA E  latched, acknowledged, needs CMD_*_RESET
	6 SHELVED         ISA F  hidden by an operator for a limited time
	7 SUPPRESSED      ISA G  hidden by design
	8 OUT_OF_SERVICE  ISA H  removed from service, e.g. for maintenance

HIDING
	While shelved, suppressed or out of service the alarm is neither shown nor
	counted, and its state rests at NORMAL. When it is shown again with the
	signal still in alarm, it starts over as ACTIVE_UNACK, so nothing that
	happened while it was hidden can go unnoticed.

REQUESTS
	CMD_OP_* come from an HMI and are cleared by this block once handled.
	CMD_PG_* come from logic and are taken on their rising edge.
	There is no CMD_PG_SHELVE: only an operator may shelve (ISA-18.2).
	If a request and its opposite arrive together, ENABLE, UNSUPPRESS and
	UNSHELVE take effect.

VAL_DIAG bits
	.0 configuration fault (.2, .3 or .4)  .4 chatter settings invalid
	.1 IN_FAULT is set                     .5 last shelve request refused
	.2 CFG_SEVERITY not in 1..1000         .6 chattering
	.3 a negative delay

RESTART
	Nothing here is retained. After a cold restart a signal that is still in
	alarm is announced again as ACTIVE_UNACK, and any shelve is gone.
==========================================================================*)
VAR_INPUT
	(* PROCESS *)
	IN_ENABLE: BOOL := TRUE;		// FALSE: THE BLOCK SKIPS ITS LOGIC AND OUTPUTS KEEP THEIR VALUES
	IN_SIGNAL: BOOL;				// THE SIGNAL BEING ALARMED
	IN_FAULT: BOOL;					// SIGNAL QUALITY IS BAD: KEEP THE LAST GOOD ALARM CONDITION
	CFG_INVERT: BOOL;				// FALSE: ALARM WHILE IN_SIGNAL IS TRUE. TRUE: ALARM WHILE IT IS FALSE

	(* CONFIGURATION *)
	CFG_ACK_REQ: BOOL;				// TRUE: AN OPERATOR MUST ACKNOWLEDGE
	CFG_LATCH: BOOL;				// TRUE: STAYS IN ALARM AFTER RETURN TO NORMAL UNTIL RESET
	CFG_ON_DELAY_MS: DINT;			// THE SIGNAL MUST BE IN ALARM THIS LONG (ms) BEFORE IT IS ANNOUNCED
	CFG_OFF_DELAY_MS: DINT;			// ...AND NORMAL THIS LONG (ms) BEFORE IT RETURNS TO NORMAL
	CFG_SEVERITY: DINT := 500;		// 1..1000; VAL_PRIORITY IS DERIVED FROM IT
	CFG_SHELVE_MAX: TIME;			// UPPER LIMIT FOR ANY SHELVE. T#0S: SHELVING IS NOT ALLOWED
	CMD_SHELVE_TIME: TIME;			// LENGTH OF THE NEXT SHELVE. T#0S: USE CFG_SHELVE_MAX
	CMD_SHELVE_ONESHOT: BOOL;		// THE NEXT SHELVE ALSO ENDS WHEN THE SIGNAL RETURNS TO NORMAL
	CFG_CHATTER_COUNT: DINT;		// THIS MANY ACTIVATIONS IN ONE CFG_CHATTER_WINDOW = CHATTERING. 0: OFF
	CFG_CHATTER_WINDOW: TIME := T#60S;

	(* TIME STAMPING *)
	IN_CLOCK: DT;					// WALL-CLOCK DATE AND TIME OF THE CONTROLLER
	CFG_USE_SRC_TIME: BOOL;			// TRUE: STAMP EVENTS WITH IN_SRC_TIME INSTEAD OF IN_CLOCK
	IN_SRC_TIME: DT;				// E.G. THE TIME AN SOE INPUT MODULE SAW THE CHANGE

	(* OPERATOR REQUESTS - ONE-SHOT, CLEARED BY THE BLOCK *)
	CMD_OP_ACK: BOOL;
	CMD_OP_RESET: BOOL;
	CMD_OP_DISABLE: BOOL;
	CMD_OP_ENABLE: BOOL;
	CMD_OP_SUPPRESS: BOOL;
	CMD_OP_UNSUPPRESS: BOOL;
	CMD_OP_SHELVE: BOOL;
	CMD_OP_UNSHELVE: BOOL;

	(* LOGIC REQUESTS - RISING EDGE *)
	CMD_PG_ACK: BOOL;
	CMD_PG_RESET: BOOL;
	CMD_PG_DISABLE: BOOL;
	CMD_PG_ENABLE: BOOL;
	CMD_PG_SUPPRESS: BOOL;
	CMD_PG_UNSUPPRESS: BOOL;
	CMD_PG_UNSHELVE: BOOL;

	CMD_COUNT_RESET: BOOL;		// RISING EDGE
END_VAR
VAR_OUTPUT
	STS_ENABLED: BOOL;
	STS_ALM: BOOL;					// IN ALARM OR LATCHED (VAL_STATE 1, 2, 4, 5)
	STS_ACKED: BOOL;				// NOTHING WAITS FOR AN ACKNOWLEDGEMENT
	STS_ALM_UNACK: BOOL;			// STS_ALM AND NOT STS_ACKED
	STS_UNACKED: BOOL;				// SOMETHING WAITS FOR AN ACKNOWLEDGEMENT, RTN_UNACK INCLUDED (HORN)
	STS_COND: BOOL;					// FILTERED ALARM CONDITION, REPORTED EVEN WHILE HIDDEN
	STS_SUPPRESSED: BOOL;
	STS_SHELVED: BOOL;
	STS_DISABLED: BOOL;
	STS_CHATTER: BOOL;
	STS_IN_FAULT: BOOL;
	STS_CFG_FAULT: BOOL;
	VAL_PRIORITY: DINT;				// 1 URGENT, 2 HIGH, 3 MEDIUM, 4 LOW
	VAL_COUNT: DINT;
	VAL_STATE: DINT;
	VAL_DIAG: DINT;

	TS_ALM: DT;
	TS_ACK: DT;
	TS_RTN: DT;
	TS_RESET: DT;
	TS_COUNT_RESET: DT;
	TS_SHELVE: DT;
	TS_UNSHELVE: DT;
	TS_SHELVE_EXPIRY: DT;
	VAL_SHELVE_LEFT: TIME;

	VAL_ON_DELAY_MS: DINT;			// TIME (ms) THE ON-DELAY HAS RUN SO FAR
	CFG_VERSION: REAL;
END_VAR
VAR
	ALMSTATE: DINT;				// BASE STATE 0..5
	NOW: DT;
	RAWCOND: BOOL;
	LASTGOODCOND: BOOL;
	COND: BOOL;					// CONDITION AFTER ON/OFF DELAY
	SHELVEPT: TIME;
	SHELVEDONESHOT: BOOL;
	SHELVEREJECTED: BOOL;
	CHATTERACC: DINT;

	ACKCMD: BOOL;
	RESETCMD: BOOL;
	DISABLECMD: BOOL;
	ENABLECMD: BOOL;
	SUPPRESSCMD: BOOL;
	UNSUPPRESSCMD: BOOL;
	SHELVECMD: BOOL;
	UNSHELVECMD: BOOL;

	ON_TMR: TON;
	OFF_TMR: TON;
	SHELVE_TMR: TON;
	CHATTER_TMR: TON;
	COND_RISE: R_TRIG;
	COND_FALL: F_TRIG;
	PG_ACK_ONS: R_TRIG;
	PG_RESET_ONS: R_TRIG;
	PG_DISABLE_ONS: R_TRIG;
	PG_ENABLE_ONS: R_TRIG;
	PG_SUPPRESS_ONS: R_TRIG;
	PG_UNSUPPRESS_ONS: R_TRIG;
	PG_UNSHELVE_ONS: R_TRIG;
	COUNTRESET_ONS: R_TRIG;
END_VAR
VAR CONSTANT
	ST_NORMAL: DINT := 0;
	ST_ACTIVE_UNACK: DINT := 1;
	ST_ACTIVE_ACK: DINT := 2;
	ST_RTN_UNACK: DINT := 3;
	ST_LATCHED_UNACK: DINT := 4;
	ST_LATCHED_ACK: DINT := 5;
	ST_SHELVED: DINT := 6;
	ST_SUPPRESSED: DINT := 7;
	ST_OUT_OF_SERVICE: DINT := 8;
END_VAR

(*==========================================================================
Sets the Version of the Control Module on the HMI and in the Program.
==========================================================================
Date Last Updated: 10-1-2026
===========================================================================*)
//CFG_VERSION:= 1.0; // ORIGINAL
//CFG_VERSION := 2.0; //FIRST ALARM BLOCK (ARCHIVED AS archive/digital_alarm_v2.st)
//CFG_VERSION := 3.0; //ISA-18.2 STATE MODEL, SHELVING, OUT OF SERVICE, OFF-DELAY, CHATTER, DT TIME STAMPS
CFG_VERSION := 3.1; //RENAMED BG_ALM_DIG WITH IN_/CFG_/CMD_/STS_/VAL_/TS_ NAMES; NO LOGIC CHANGE

STS_ENABLED := IN_ENABLE;
IF NOT IN_ENABLE THEN
	JMP CLEAR_COMMANDS;
END_IF

IF CFG_USE_SRC_TIME THEN
	NOW := IN_SRC_TIME;
ELSE
	NOW := IN_CLOCK;
END_IF

(*===========================================================================
CONFIGURATION - Validate and map severity to priority
===========================================================================*)
VAL_DIAG := 0;
VAL_DIAG.2 := CFG_SEVERITY < 1 OR CFG_SEVERITY > 1000;
VAL_DIAG.3 := CFG_ON_DELAY_MS < 0 OR CFG_OFF_DELAY_MS < 0;
VAL_DIAG.4 := CFG_CHATTER_COUNT < 0 OR (CFG_CHATTER_COUNT > 0 AND CFG_CHATTER_WINDOW = T#0S);
STS_CFG_FAULT := VAL_DIAG.2 OR VAL_DIAG.3 OR VAL_DIAG.4;

IF CFG_SEVERITY <= 250 THEN
	VAL_PRIORITY := 4;
ELSIF CFG_SEVERITY <= 500 THEN
	VAL_PRIORITY := 3;
ELSIF CFG_SEVERITY <= 750 THEN
	VAL_PRIORITY := 2;
ELSE
	VAL_PRIORITY := 1;
END_IF

(*===========================================================================
CONDITION - Invert, hold on bad quality, then apply the on-delay and off-delay
===========================================================================*)
RAWCOND := IN_SIGNAL XOR CFG_INVERT;
STS_IN_FAULT := IN_FAULT;
IF IN_FAULT THEN
	RAWCOND := LASTGOODCOND;	// BAD DATA MUST NOT RAISE OR CLEAR THE ALARM
ELSE
	LASTGOODCOND := RAWCOND;
END_IF

ON_TMR(IN := RAWCOND AND NOT COND, PT := DINT_TO_TIME(MAX(CFG_ON_DELAY_MS, 0)));
OFF_TMR(IN := NOT RAWCOND AND COND, PT := DINT_TO_TIME(MAX(CFG_OFF_DELAY_MS, 0)));
IF ON_TMR.Q THEN
	COND := TRUE;
ELSIF OFF_TMR.Q THEN
	COND := FALSE;
END_IF
VAL_ON_DELAY_MS := TIME_TO_DINT(ON_TMR.ET);
STS_COND := COND;
COND_RISE(CLK := COND);
COND_FALL(CLK := COND);

(*===========================================================================
REQUESTS - Operator one-shots and logic rising edges
===========================================================================*)
PG_ACK_ONS(CLK := CMD_PG_ACK);
PG_RESET_ONS(CLK := CMD_PG_RESET);
PG_DISABLE_ONS(CLK := CMD_PG_DISABLE);
PG_ENABLE_ONS(CLK := CMD_PG_ENABLE);
PG_SUPPRESS_ONS(CLK := CMD_PG_SUPPRESS);
PG_UNSUPPRESS_ONS(CLK := CMD_PG_UNSUPPRESS);
PG_UNSHELVE_ONS(CLK := CMD_PG_UNSHELVE);

ACKCMD := CMD_OP_ACK OR PG_ACK_ONS.Q;
RESETCMD := CMD_OP_RESET OR PG_RESET_ONS.Q;
DISABLECMD := CMD_OP_DISABLE OR PG_DISABLE_ONS.Q;
ENABLECMD := CMD_OP_ENABLE OR PG_ENABLE_ONS.Q;
SUPPRESSCMD := CMD_OP_SUPPRESS OR PG_SUPPRESS_ONS.Q;
UNSUPPRESSCMD := CMD_OP_UNSUPPRESS OR PG_UNSUPPRESS_ONS.Q;
SHELVECMD := CMD_OP_SHELVE;
UNSHELVECMD := CMD_OP_UNSHELVE OR PG_UNSHELVE_ONS.Q;

(*===========================================================================
OUT OF SERVICE (ISA H) - Supersedes shelving
===========================================================================*)
IF DISABLECMD AND NOT STS_DISABLED THEN
	STS_DISABLED := TRUE;
	IF STS_SHELVED THEN
		STS_SHELVED := FALSE;
		TS_UNSHELVE := NOW;
	END_IF
END_IF
IF ENABLECMD THEN
	STS_DISABLED := FALSE;
END_IF

(*===========================================================================
SUPPRESSED BY DESIGN (ISA G)
===========================================================================*)
IF SUPPRESSCMD THEN
	STS_SUPPRESSED := TRUE;
END_IF
IF UNSUPPRESSCMD THEN
	STS_SUPPRESSED := FALSE;
END_IF

(*===========================================================================
SHELVING (ISA F) - Operator only, always time-limited by CFG_SHELVE_MAX.
A shelve request while shelved restarts the timer with the new duration.
===========================================================================*)
IF SHELVECMD THEN
	IF STS_DISABLED OR CFG_SHELVE_MAX = T#0S OR (CMD_SHELVE_ONESHOT AND NOT COND) THEN
		SHELVEREJECTED := TRUE;
	ELSE
		IF CMD_SHELVE_TIME = T#0S OR CMD_SHELVE_TIME > CFG_SHELVE_MAX THEN
			SHELVEPT := CFG_SHELVE_MAX;
		ELSE
			SHELVEPT := CMD_SHELVE_TIME;
		END_IF
		STS_SHELVED := TRUE;
		SHELVEDONESHOT := CMD_SHELVE_ONESHOT;
		SHELVEREJECTED := FALSE;
		TS_SHELVE := NOW;
		TS_SHELVE_EXPIRY := ADD_DT_TIME(NOW, SHELVEPT);
		SHELVE_TMR(IN := FALSE);
	END_IF
END_IF

SHELVE_TMR(IN := STS_SHELVED, PT := SHELVEPT);
IF STS_SHELVED AND (UNSHELVECMD OR SHELVE_TMR.Q OR (SHELVEDONESHOT AND COND_FALL.Q)) THEN
	STS_SHELVED := FALSE;
	TS_UNSHELVE := NOW;
	SHELVE_TMR(IN := FALSE);
END_IF

IF STS_SHELVED THEN
	VAL_SHELVE_LEFT := SHELVEPT - SHELVE_TMR.ET;
ELSE
	VAL_SHELVE_LEFT := T#0S;
END_IF

(*===========================================================================
ALARM STATE MACHINE (ISA A-E)
While shelved, suppressed or out of service the alarm is held at NORMAL, so an
active condition re-annunciates as ACTIVE_UNACK when the alarm is returned.
===========================================================================*)
IF STS_DISABLED OR STS_SUPPRESSED OR STS_SHELVED THEN
	ALMSTATE := ST_NORMAL;
ELSE
	// CONDITION CHANGES
	CASE ALMSTATE OF
	ST_NORMAL, ST_RTN_UNACK, ST_LATCHED_UNACK, ST_LATCHED_ACK:
		IF COND THEN
			ALMSTATE := ST_ACTIVE_UNACK;
			VAL_COUNT := VAL_COUNT + 1;
			TS_ALM := NOW;
		END_IF
	ST_ACTIVE_UNACK:
		IF NOT COND THEN
			TS_RTN := NOW;
			IF CFG_LATCH THEN
				ALMSTATE := ST_LATCHED_UNACK;
			ELSE
				ALMSTATE := ST_RTN_UNACK;
			END_IF
		END_IF
	ST_ACTIVE_ACK:
		IF NOT COND THEN
			TS_RTN := NOW;
			IF CFG_LATCH THEN
				ALMSTATE := ST_LATCHED_ACK;
			ELSE
				ALMSTATE := ST_NORMAL;
			END_IF
		END_IF
	END_CASE

	// ACKNOWLEDGE - IMPLICIT WHEN NO ACKNOWLEDGEMENT IS REQUIRED
	IF ACKCMD OR NOT CFG_ACK_REQ THEN
		CASE ALMSTATE OF
		ST_ACTIVE_UNACK:
			ALMSTATE := ST_ACTIVE_ACK;
			TS_ACK := NOW;
		ST_RTN_UNACK:
			ALMSTATE := ST_NORMAL;
			TS_ACK := NOW;
		ST_LATCHED_UNACK:
			ALMSTATE := ST_LATCHED_ACK;
			TS_ACK := NOW;
		END_CASE
	END_IF

	// RESET - ONLY A LATCHED ALARM THAT IS BACK TO NORMAL AND ACKNOWLEDGED.
	// AN ACK AND RESET IN THE SAME SCAN CLEARS A LATCHED_UNACK ALARM.
	IF RESETCMD AND ALMSTATE = ST_LATCHED_ACK THEN
		ALMSTATE := ST_NORMAL;
		TS_RESET := NOW;
	END_IF
END_IF

(*===========================================================================
CHATTER - CFG_CHATTER_COUNT or more activations within one CFG_CHATTER_WINDOW.
Cleared at the end of a window with fewer activations.
===========================================================================*)
IF CFG_CHATTER_COUNT > 0 THEN
	CHATTER_TMR(IN := NOT CHATTER_TMR.Q, PT := CFG_CHATTER_WINDOW);
	IF COND_RISE.Q THEN
		CHATTERACC := CHATTERACC + 1;
	END_IF
	IF CHATTERACC >= CFG_CHATTER_COUNT THEN
		STS_CHATTER := TRUE;
	END_IF
	IF CHATTER_TMR.Q THEN
		STS_CHATTER := CHATTERACC >= CFG_CHATTER_COUNT;
		CHATTERACC := 0;
	END_IF
ELSE
	CHATTER_TMR(IN := FALSE);
	CHATTERACC := 0;
	STS_CHATTER := FALSE;
END_IF

(*===========================================================================
ALARM COUNT
===========================================================================*)
COUNTRESET_ONS(CLK := CMD_COUNT_RESET);
IF COUNTRESET_ONS.Q THEN
	VAL_COUNT := 0;
	TS_COUNT_RESET := NOW;
END_IF

(*===========================================================================
OUTPUT UPDATES
===========================================================================*)
STS_ALM := ALMSTATE = ST_ACTIVE_UNACK OR ALMSTATE = ST_ACTIVE_ACK
	OR ALMSTATE = ST_LATCHED_UNACK OR ALMSTATE = ST_LATCHED_ACK;
STS_UNACKED := ALMSTATE = ST_ACTIVE_UNACK OR ALMSTATE = ST_RTN_UNACK OR ALMSTATE = ST_LATCHED_UNACK;
STS_ACKED := NOT STS_UNACKED;
STS_ALM_UNACK := STS_ALM AND STS_UNACKED;

IF STS_DISABLED THEN
	VAL_STATE := ST_OUT_OF_SERVICE;
ELSIF STS_SUPPRESSED THEN
	VAL_STATE := ST_SUPPRESSED;
ELSIF STS_SHELVED THEN
	VAL_STATE := ST_SHELVED;
ELSE
	VAL_STATE := ALMSTATE;
END_IF

VAL_DIAG.0 := STS_CFG_FAULT;
VAL_DIAG.1 := IN_FAULT;
VAL_DIAG.5 := SHELVEREJECTED;
VAL_DIAG.6 := STS_CHATTER;

(*===========================================================================
CLEAR OPERATOR REQUESTS - They are one-shot
===========================================================================*)
CLEAR_COMMANDS:
CMD_OP_ACK := FALSE;
CMD_OP_RESET := FALSE;
CMD_OP_DISABLE := FALSE;
CMD_OP_ENABLE := FALSE;
CMD_OP_SUPPRESS := FALSE;
CMD_OP_UNSUPPRESS := FALSE;
CMD_OP_SHELVE := FALSE;
CMD_OP_UNSHELVE := FALSE;

END_FUNCTION_BLOCK
