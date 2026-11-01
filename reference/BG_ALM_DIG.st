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
	almstate: DINT;				// BASE STATE 0..5
	now: DT;
	rawcond: BOOL;
	lastgoodcond: BOOL;
	cond: BOOL;					// CONDITION AFTER ON/OFF DELAY
	shelvept: TIME;
	shelvedoneshot: BOOL;
	shelverejected: BOOL;
	chatteracc: DINT;

	ackcmd: BOOL;
	resetcmd: BOOL;
	disablecmd: BOOL;
	enablecmd: BOOL;
	suppresscmd: BOOL;
	unsuppresscmd: BOOL;
	shelvecmd: BOOL;
	unshelvecmd: BOOL;

	on_tmr: TON;
	off_tmr: TON;
	shelve_tmr: TON;
	chatter_tmr: TON;
	cond_rise: R_TRIG;
	cond_fall: F_TRIG;
	pg_ack_ons: R_TRIG;
	pg_reset_ons: R_TRIG;
	pg_disable_ons: R_TRIG;
	pg_enable_ons: R_TRIG;
	pg_suppress_ons: R_TRIG;
	pg_unsuppress_ons: R_TRIG;
	pg_unshelve_ons: R_TRIG;
	countreset_ons: R_TRIG;
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
	now := IN_SRC_TIME;
ELSE
	now := IN_CLOCK;
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
rawcond := IN_SIGNAL XOR CFG_INVERT;
STS_IN_FAULT := IN_FAULT;
IF IN_FAULT THEN
	rawcond := lastgoodcond;	// BAD DATA MUST NOT RAISE OR CLEAR THE ALARM
ELSE
	lastgoodcond := rawcond;
END_IF

on_tmr(IN := rawcond AND NOT cond, PT := DINT_TO_TIME(MAX(CFG_ON_DELAY_MS, 0)));
off_tmr(IN := NOT rawcond AND cond, PT := DINT_TO_TIME(MAX(CFG_OFF_DELAY_MS, 0)));
IF on_tmr.Q THEN
	cond := TRUE;
ELSIF off_tmr.Q THEN
	cond := FALSE;
END_IF
VAL_ON_DELAY_MS := TIME_TO_DINT(on_tmr.ET);
STS_COND := cond;
cond_rise(CLK := cond);
cond_fall(CLK := cond);

(*===========================================================================
REQUESTS - Operator one-shots and logic rising edges
===========================================================================*)
pg_ack_ons(CLK := CMD_PG_ACK);
pg_reset_ons(CLK := CMD_PG_RESET);
pg_disable_ons(CLK := CMD_PG_DISABLE);
pg_enable_ons(CLK := CMD_PG_ENABLE);
pg_suppress_ons(CLK := CMD_PG_SUPPRESS);
pg_unsuppress_ons(CLK := CMD_PG_UNSUPPRESS);
pg_unshelve_ons(CLK := CMD_PG_UNSHELVE);

ackcmd := CMD_OP_ACK OR pg_ack_ons.Q;
resetcmd := CMD_OP_RESET OR pg_reset_ons.Q;
disablecmd := CMD_OP_DISABLE OR pg_disable_ons.Q;
enablecmd := CMD_OP_ENABLE OR pg_enable_ons.Q;
suppresscmd := CMD_OP_SUPPRESS OR pg_suppress_ons.Q;
unsuppresscmd := CMD_OP_UNSUPPRESS OR pg_unsuppress_ons.Q;
shelvecmd := CMD_OP_SHELVE;
unshelvecmd := CMD_OP_UNSHELVE OR pg_unshelve_ons.Q;

(*===========================================================================
OUT OF SERVICE (ISA H) - Supersedes shelving
===========================================================================*)
IF disablecmd AND NOT STS_DISABLED THEN
	STS_DISABLED := TRUE;
	IF STS_SHELVED THEN
		STS_SHELVED := FALSE;
		TS_UNSHELVE := now;
	END_IF
END_IF
IF enablecmd THEN
	STS_DISABLED := FALSE;
END_IF

(*===========================================================================
SUPPRESSED BY DESIGN (ISA G)
===========================================================================*)
IF suppresscmd THEN
	STS_SUPPRESSED := TRUE;
END_IF
IF unsuppresscmd THEN
	STS_SUPPRESSED := FALSE;
END_IF

(*===========================================================================
SHELVING (ISA F) - Operator only, always time-limited by CFG_SHELVE_MAX.
A shelve request while shelved restarts the timer with the new duration.
===========================================================================*)
IF shelvecmd THEN
	IF STS_DISABLED OR CFG_SHELVE_MAX = T#0S OR (CMD_SHELVE_ONESHOT AND NOT cond) THEN
		shelverejected := TRUE;
	ELSE
		IF CMD_SHELVE_TIME = T#0S OR CMD_SHELVE_TIME > CFG_SHELVE_MAX THEN
			shelvept := CFG_SHELVE_MAX;
		ELSE
			shelvept := CMD_SHELVE_TIME;
		END_IF
		STS_SHELVED := TRUE;
		shelvedoneshot := CMD_SHELVE_ONESHOT;
		shelverejected := FALSE;
		TS_SHELVE := now;
		TS_SHELVE_EXPIRY := ADD_DT_TIME(now, shelvept);
		shelve_tmr(IN := FALSE);
	END_IF
END_IF

shelve_tmr(IN := STS_SHELVED, PT := shelvept);
IF STS_SHELVED AND (unshelvecmd OR shelve_tmr.Q OR (shelvedoneshot AND cond_fall.Q)) THEN
	STS_SHELVED := FALSE;
	TS_UNSHELVE := now;
	shelve_tmr(IN := FALSE);
END_IF

IF STS_SHELVED THEN
	VAL_SHELVE_LEFT := shelvept - shelve_tmr.ET;
ELSE
	VAL_SHELVE_LEFT := T#0S;
END_IF

(*===========================================================================
ALARM STATE MACHINE (ISA A-E)
While shelved, suppressed or out of service the alarm is held at NORMAL, so an
active condition re-annunciates as ACTIVE_UNACK when the alarm is returned.
===========================================================================*)
IF STS_DISABLED OR STS_SUPPRESSED OR STS_SHELVED THEN
	almstate := ST_NORMAL;
ELSE
	// CONDITION CHANGES
	CASE almstate OF
	ST_NORMAL, ST_RTN_UNACK, ST_LATCHED_UNACK, ST_LATCHED_ACK:
		IF cond THEN
			almstate := ST_ACTIVE_UNACK;
			VAL_COUNT := VAL_COUNT + 1;
			TS_ALM := now;
		END_IF
	ST_ACTIVE_UNACK:
		IF NOT cond THEN
			TS_RTN := now;
			IF CFG_LATCH THEN
				almstate := ST_LATCHED_UNACK;
			ELSE
				almstate := ST_RTN_UNACK;
			END_IF
		END_IF
	ST_ACTIVE_ACK:
		IF NOT cond THEN
			TS_RTN := now;
			IF CFG_LATCH THEN
				almstate := ST_LATCHED_ACK;
			ELSE
				almstate := ST_NORMAL;
			END_IF
		END_IF
	END_CASE

	// ACKNOWLEDGE - IMPLICIT WHEN NO ACKNOWLEDGEMENT IS REQUIRED
	IF ackcmd OR NOT CFG_ACK_REQ THEN
		CASE almstate OF
		ST_ACTIVE_UNACK:
			almstate := ST_ACTIVE_ACK;
			TS_ACK := now;
		ST_RTN_UNACK:
			almstate := ST_NORMAL;
			TS_ACK := now;
		ST_LATCHED_UNACK:
			almstate := ST_LATCHED_ACK;
			TS_ACK := now;
		END_CASE
	END_IF

	// RESET - ONLY A LATCHED ALARM THAT IS BACK TO NORMAL AND ACKNOWLEDGED.
	// AN ACK AND RESET IN THE SAME SCAN CLEARS A LATCHED_UNACK ALARM.
	IF resetcmd AND almstate = ST_LATCHED_ACK THEN
		almstate := ST_NORMAL;
		TS_RESET := now;
	END_IF
END_IF

(*===========================================================================
CHATTER - CFG_CHATTER_COUNT or more activations within one CFG_CHATTER_WINDOW.
Cleared at the end of a window with fewer activations.
===========================================================================*)
IF CFG_CHATTER_COUNT > 0 THEN
	chatter_tmr(IN := NOT chatter_tmr.Q, PT := CFG_CHATTER_WINDOW);
	IF cond_rise.Q THEN
		chatteracc := chatteracc + 1;
	END_IF
	IF chatteracc >= CFG_CHATTER_COUNT THEN
		STS_CHATTER := TRUE;
	END_IF
	IF chatter_tmr.Q THEN
		STS_CHATTER := chatteracc >= CFG_CHATTER_COUNT;
		chatteracc := 0;
	END_IF
ELSE
	chatter_tmr(IN := FALSE);
	chatteracc := 0;
	STS_CHATTER := FALSE;
END_IF

(*===========================================================================
ALARM COUNT
===========================================================================*)
countreset_ons(CLK := CMD_COUNT_RESET);
IF countreset_ons.Q THEN
	VAL_COUNT := 0;
	TS_COUNT_RESET := now;
END_IF

(*===========================================================================
OUTPUT UPDATES
===========================================================================*)
STS_ALM := almstate = ST_ACTIVE_UNACK OR almstate = ST_ACTIVE_ACK
	OR almstate = ST_LATCHED_UNACK OR almstate = ST_LATCHED_ACK;
STS_UNACKED := almstate = ST_ACTIVE_UNACK OR almstate = ST_RTN_UNACK OR almstate = ST_LATCHED_UNACK;
STS_ACKED := NOT STS_UNACKED;
STS_ALM_UNACK := STS_ALM AND STS_UNACKED;

IF STS_DISABLED THEN
	VAL_STATE := ST_OUT_OF_SERVICE;
ELSIF STS_SUPPRESSED THEN
	VAL_STATE := ST_SUPPRESSED;
ELSIF STS_SHELVED THEN
	VAL_STATE := ST_SHELVED;
ELSE
	VAL_STATE := almstate;
END_IF

VAL_DIAG.0 := STS_CFG_FAULT;
VAL_DIAG.1 := IN_FAULT;
VAL_DIAG.5 := shelverejected;
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
