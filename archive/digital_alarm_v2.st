FUNCTION_BLOCK AOI_CM_ALMD
(*==========================================================================
v2.0 of the digital alarm block, kept as a historical record. It was replaced
by v3.0, now BG_ALM_DIG in reference/digital_alarm.st. The code below is the
original v2.0 and is not maintained; its block and parameter names are the
originals and are not used anywhere else.

WHAT v2.0 SET OUT TO DO
	Raise an alarm from one BOOL signal (IN), optionally inverted (CONDITION =
	TRUE), with an optional minimum time in alarm (MINDURATIONPRE, ms), an
	optional acknowledge requirement (ACKREQUIRED) and optional latching until
	a reset (LATCHED). Operator requests (OPER*) and logic requests (PROG*)
	were meant to acknowledge, reset, take the alarm out of service, suppress
	it and shelve it, the last three corresponding to ISA-18.2 states H, G and
	F. Outputs report whether the alarm is in alarm and acknowledged, how often
	it has activated, and when (INALARMTIME, ACKTIME, RETONORMALTIME, ...).
	STATE reported ISA-18.2 states A to D: 0 normal, 1 in alarm unacknowledged,
	2 in alarm acknowledged, 3 back to normal unacknowledged.

WHY IT WAS REPLACED - DEFECTS FOUND IN REVIEW
	- INALARM := MINDURATION.Q is overwritten by INALARM := IN, so the minimum
	  duration has no effect on INALARM.
	- CONDITION only inverts INALARM; the state transitions test the raw IN.
	- STATE is recomputed from INALARM/ACKED every scan, so the block does not
	  remember its state and latching does not hold.
	- Latched states 1 and 2 clear on NOT IN, and state 3 returns to NORMAL with
	  no acknowledgement.
	- A reset sets ACKED, and the acknowledge test in the same scan then moves
	  the alarm to state 2 (INALARM TRUE). The non-latched RTN path has the same
	  problem going from state 3 to state 2.
	- Suppress writes to the disable inputs, and the command resets at the end
	  cancel opposing pairs, so suppress and disable are mixed up.
	- Shelving is declared but not implemented: OPERSHELVE, OPERUNSHELVE,
	  PROGSHELVE, PROGUNSHELVE, MAXSHELVEDURATION and SEVERITY are never read;
	  SHELVED, SUPPRESSED, DIABLED, ALARMCOUNT, INALARMUNACK and INSTRUCTFAULT
	  are never written.
	- PROGTIME is overwritten from M_TX; the time stamps are TIME durations from
	  OSCAT M_TX, not date-times, and ACKTIME is the "time high", not the ack time.
	- Only OPERACK and PROGACK are cleared; PROG* commands are level, not edge.
	- MINDURATIONACC is an INT and overflows after 32.767 s. The timer
	  instances are declared as outputs.
	- Typos: DIABLED, OPERDISBLE, PROGUNSUPRESS, USEPROTIME (duplicate of USEPROGTIME).
==========================================================================*)
VAR_INPUT
	(* INPUTS *)
	ENABLEIN: BOOL;
	IN: BOOL; //DIGITAL ALARM
	INFAULT: BOOL;
	CONDITION: BOOL; //INVERT INPUT SIGNAL 
	ACKREQUIRED: BOOL; // ACKKNOWLEDGE REQUIREMENT
	LATCHED: BOOL;	//LATCH REQUIREMENT 
	MINDURATIONPRE: DINT;
	
	OPERACK: BOOL;
	OPERRESET: BOOL;
	OPERDISBLE: BOOL;
	OPERENABLE: BOOL;
	OPERSUPPRESS: BOOL;
	OPERUNSUPPRESS: BOOL;
	OPERSHELVE: BOOL;
	OPERUNSHELVE: BOOL;
	
	PROGACK: BOOL;
	PROGRESET: BOOL;
	PROGDISABLE: BOOL;
	PROGENABLE: BOOL;
	PROGSUPPRESS: BOOL;
	PROGUNSUPRESS: BOOL;
	PROGSHELVE: BOOL;
	PROGUNSHELVE: BOOL;

	ALARMCOUNTRESET: BOOL;
	USEPROGTIME: BOOL;
	SEVERITY: DINT;
	MAXSHELVEDURATION: DINT;
	
	USEPROTIME: BOOL;
	PROGTIME: TIME;
END_VAR
VAR_OUTPUT
	ENABLEOUT: BOOL;
	INALARM: BOOL;
	ACKED: BOOL;
	INALARMUNACK: BOOL;
	SUPPRESSED: BOOL;
	SHELVED: BOOL;
	DIABLED: BOOL;
	INSTRUCTFAULT: BOOL;
	ALARMCOUNT: DINT;
	
	INALARMTIME: TIME;
	ACKTIME: TIME;
	RETONORMALTIME: TIME;
	ALARMCOUNTRESETTIME: TIME;

	SHELVETIME: TIME;
	UNSHELVETIME: TIME;
	
	STATE: DINT;
	STATUS: DINT;
	
	MINDURATIONACC: INT;
	INFAULTED: BOOL;

	MINDURATION: TON;
	ALARMTIME: OSCAT_BASIC.M_TX;
	CFG_VERSION: REAL;
END_VAR

(*==========================================================================
Sets the Version of the Control Module on the HMI and in the Program.
==========================================================================
Date Last Updated: 5-27-2024
===========================================================================*)
//CFG_VERSION:= 1.0; // ORIGINAL
CFG_VERSION := 2.0; //ADDED AOI_CM_ALMD FUNCTION BLOCK
MINDURATION(IN:=IN, PT:=DINT_TO_TIME(MINDURATIONPRE));
MINDURATIONACC := TIME_TO_INT(MINDURATION.ET);
INALARM := MINDURATION.Q;
ALARMTIME(in:= IN,rst:=ALARMCOUNTRESET);

IF NOT CONDITION THEN
	INALARM := IN;
ELSE 
	INALARM := NOT IN;	
END_IF

IF OPERSUPPRESS THEN
	OPERDISBLE := TRUE;
	OPERENABLE := FALSE;
END_IF

IF OPERUNSUPPRESS THEN
	OPERDISBLE := FALSE;
	OPERENABLE := TRUE;
END_IF

IF PROGSUPPRESS THEN
	PROGDISABLE := TRUE;
	PROGENABLE := FALSE;
END_IF

IF PROGUNSUPRESS THEN
	PROGDISABLE := FALSE;
	PROGENABLE := TRUE;
END_IF

IF ENABLEIN AND NOT (OPERSUPPRESS OR PROGSUPPRESS) THEN
	ENABLEOUT := TRUE;
ELSE
	ENABLEOUT := FALSE;
END_IF


IF ACKREQUIRED THEN
	IF NOT INALARM AND ACKED THEN
		STATE := 0;
	ELSIF INALARM AND NOT ACKED THEN
		STATE := 1;
	ELSIF INALARM AND ACKED THEN
		STATE := 2;
	ELSIF NOT INALARM AND NOT ACKED THEN
		STATE := 3;
	ELSE 
		//DO NOTHING
	END_IF

	IF LATCHED THEN
		CASE STATE OF
		0:
			IF IN AND MINDURATION.Q THEN
				STATE := 1;
				INALARM := TRUE;
				ACKED := FALSE;
			END_IF
		1:
			IF NOT IN OR OPERRESET OR PROGRESET THEN
				STATE := 0;
				INALARM := FALSE;
				ACKED := TRUE;
			END_IF
			
			IF ACKED OR PROGACK OR OPERACK THEN
				STATE := 2;
				INALARM := TRUE;
				ACKED := TRUE;
			END_IF
		2:
			IF NOT IN OR OPERRESET OR PROGRESET THEN
				STATE := 0;
				INALARM := FALSE;
				ACKED := TRUE;
			END_IF
		3:
			STATE := 0;
		END_CASE
	
	ELSE
		CASE STATE OF
		0:
			IF IN AND MINDURATION.Q THEN
				STATE := 1;
				INALARM := TRUE;
				ACKED := FALSE;
			END_IF
		1:
			IF NOT IN THEN
				STATE := 3;
				INALARM := FALSE;
				ACKED := FALSE;
			END_IF
			
			IF ACKED OR PROGACK OR OPERACK THEN
				STATE := 2;
				INALARM := TRUE;
				ACKED := TRUE;
			END_IF
		2:
			IF NOT IN THEN
				STATE := 0;
				INALARM := FALSE;
				ACKED := TRUE;
			END_IF
		3:
			IF ACKED OR OPERACK OR PROGACK THEN
				STATE := 0;
				INALARM := FALSE;
				ACKED := TRUE;
			END_IF
			
			IF IN AND MINDURATION.Q THEN
				STATE := 1;
				INALARM := TRUE;
				ACKED := FALSE;
			END_IF
		END_CASE	
	END_IF
END_IF


IF NOT ACKREQUIRED THEN
	IF NOT INALARM THEN
		STATE := 0;
	ELSIF INALARM THEN
		STATE := 1;
	ELSE 
		//DO NOTHING
	END_IF
	
	IF  LATCHED THEN
		CASE STATE OF
			0:
				IF IN AND MINDURATION.Q THEN
					STATE := 1;
					INALARM := TRUE;
				END_IF
			1:
				IF NOT IN OR OPERRESET OR PROGRESET THEN
					STATE := 0;
					INALARM := FALSE;
				END_IF
		END_CASE
	ELSE
		CASE STATE OF
			0:
				IF IN AND MINDURATION.Q THEN
					STATE := 1;
					INALARM := TRUE;
				END_IF
			1:
				IF NOT IN THEN
					STATE := 0;
					INALARM := FALSE;
				END_IF
		END_CASE
	END_IF
END_IF

//OUTPUT UPDATES
PROGTIME := ALARMTIME.ET;
ACKTIME := ALARMTIME.TH;
RETONORMALTIME := ALARMTIME.TL;

IF PROGSUPPRESS THEN
	PROGUNSUPRESS := FALSE;
END_IF

IF OPERSUPPRESS THEN
	OPERUNSUPPRESS := FALSE;
END_IF

IF PROGUNSUPRESS THEN
	PROGSUPPRESS := FALSE;
END_IF

IF OPERUNSUPPRESS THEN
	OPERSUPPRESS := FALSE;
END_IF

IF INALARM AND (PROGSUPPRESS OR OPERSUPPRESS) THEN
	INALARM := FALSE;
END_IF

IF INALARM AND (PROGUNSUPRESS OR OPERUNSUPPRESS) THEN
	INALARM := TRUE;
END_IF

IF INFAULT THEN
	STATUS.1 := TRUE;
ELSE
	STATUS.1 := FALSE;
END_IF


//COMMAND RESETS
OPERACK := FALSE;
PROGACK := FALSE;

END_FUNCTION_BLOCK