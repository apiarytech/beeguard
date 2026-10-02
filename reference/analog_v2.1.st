FUNCTION_BLOCK AOI_CM_AIN
VAR_INPUT
	IN_IOH: BOOL;
	IN_INH1: DINT;
	IN_OVR: BOOL;
	IN_UNR: BOOL;
	IN_RAW: REAL;
	CMD_ALM_RES: BOOL;
END_VAR
VAR_INPUT PERSISTENT RETAIN
	CFG_EN: BOOL;
	CFG_RU_MAX: DINT;
	CFG_RU_MIN: DINT;
	CFG_EU_MAX: REAL;
	CFG_EU_MIN: REAL;
	CFG_HIHI_SP: REAL;
	CFG_HI_SP: REAL;
	CFG_LO_SP: REAL;
	CFG_LOLO_SP: REAL;
	CFG_FRZN_SP: REAL;
	CFG_DEC_PT: DINT;
	CFG_DESC: STRING;
	CFG_DEV_TYPE: DINT;
	CFG_EID: STRING;
	CFG_EN_ALM_HI: BOOL;
	CFG_EN_ALM_HIHI: BOOL;
	CFG_EN_ALM_IOH: BOOL;
	CFG_EN_ALM_LO: BOOL;
	CFG_EN_ALM_LOLO: BOOL;
	CFG_EN_ALM_OOR: BOOL;
	CFG_EN_ALM_FRZN: BOOL;
	CFG_EN_IOH: BOOL;
	CFG_EN_OOR: BOOL;
	CFG_EN_SCL: BOOL;
	CFG_EN_TOT: BOOL;
	CFG_EN_FRZN: BOOL;
	CFG_HI_DT: DINT;
	CFG_HIHI_DT: DINT;
	CFG_LO_DT: DINT;
	CFG_LOLO_DT: DINT;
	CFG_OOR_DT: DINT;
	CFG_FRZN_DT: DINT;
	CFG_SCL_TYP: DINT;
	CFG_UNITS: STRING;	
	SIM: BOOL;
	CFG_TMR_DISABLE_SIM: BOOL;
END_VAR
VAR_IN_OUT
	ALMD_HIHI: AOI_CM_ALMD;
	ALMD_HI: AOI_CM_ALMD;
	ALMD_LO: AOI_CM_ALMD;
	ALMD_LOLO: AOI_CM_ALMD;
	ALMD_OOR: AOI_CM_ALMD;
	ALMD_IOH: AOI_CM_ALMD;
	ALMD_FRZN: AOI_CM_ALMD;
END_VAR
VAR_OUTPUT
	STS_RDY2GO: BOOL;
	VAL_ACT: REAL;
	ALM_HIHI: BOOL;
	ALM_HI: BOOL;
	ALM_LO: BOOL;
	ALM_LOLO: BOOL;
	ALM_OOR: BOOL;
	ALM_IOH: BOOL;
	ALM_FRZN: BOOL;
	STS_HIHI: BOOL;
	STS_HI: BOOL;
	STS_LO: BOOL;
	STS_LOLO: BOOL;
	STS_OOR: BOOL;
	STS_IOH: BOOL;
	STS_FRZN: BOOL;
	CFG_VERSION: REAL;
	STS_ALM_ANY: BOOL;
	STS_ALM_ANY_UNACK: BOOL;
	ALM_ANY: BOOL;
	VAL_S: DINT;
END_VAR
VAR
	SIM_ONS: R_TRIG;
	WRK_B0: BOOL;
	WRK_B1: BOOL;
	WRK_ONS: ARRAY[0..15] OF R_TRIG;
	WRK_R: ARRAY[0..6] OF REAL;
	WRK_T: ARRAY[0..7] OF TON;
END_VAR



(*===========================================================================
AOI: CM_AIN
Date Last Updated: 5-27-2024
===========================================================================*)
//CFG_VERSION:= 1.0; // ORIGINAL
//CFG_VERSION := 2.0; //ADDED AOI_CM_ALMD FUNCTION BLOCK
CFG_VERSION := 2.1; //ADDED FRONZEN DATA ALARM LOGIC
(*===========================================================================
RETURN IF DISABLED  - If Module is Disabled, Scan One Last Time for Cleanup, Then Return
===========================================================================*)
IF NOT(CFG_EN) THEN 
	WRK_ONS[1].CLK := TRUE;
	IF WRK_ONS[1].Q THEN
		WRK_B1 := TRUE;
	END_IF
	IF NOT(WRK_B1) THEN
		JMP EXIT_AOI_CM_AIN;
	END_IF
ELSE
	WRK_B1 := FALSE;
	WRK_ONS[1].CLK := FALSE;
END_IF

(*===========================================================================
SETUP - Clean up the Config: Enable Alarming depending on what Inputs are Confirmed in the System.
===========================================================================*)
IF NOT(CFG_EN_IOH) THEN
	CFG_EN_ALM_IOH := FALSE;
END_IF
IF NOT(CFG_EN_OOR) THEN
	CFG_EN_ALM_OOR := FALSE;
END_IF
IF NOT(CFG_EN_FRZN) THEN
	CFG_EN_ALM_FRZN := FALSE;
END_IF
(*===========================================================================
SETUP - Prevent wire-framing in HMI during start up (this prevents dividing by zero).
===========================================================================*)
IF CFG_EU_MAX = 0.0 THEN
	CFG_EU_MAX := 100;
END_IF

(*===========================================================================
TIMER PRESETS - Check and correct for invalid Timer Preset values
===========================================================================*)
WRK_T[1].PT := DINT_TO_TIME(ABS(CFG_HIHI_DT)*1000);
WRK_T[2].PT := DINT_TO_TIME(ABS(CFG_HI_DT)*1000);
WRK_T[3].PT := DINT_TO_TIME(ABS(CFG_LO_DT)*1000);
WRK_T[4].PT := DINT_TO_TIME(ABS(CFG_LOLO_DT)*1000);
WRK_T[5].PT := DINT_TO_TIME(ABS(CFG_OOR_DT)*1000);
WRK_T[7].PT := DINT_TO_TIME(ABS(CFG_FRZN_DT)*1000);

(*===========================================================================
COMMANDS - Latch Internal Work Bit To Clear All Alarms
===========================================================================*)
SIM_ONS.CLK := SIM;
IF CMD_ALM_RES OR (SIM_ONS.Q) THEN
	WRK_B0 := TRUE;
	CMD_ALM_RES := FALSE;
END_IF

(*===========================================================================
STATUS - I/O Health
===========================================================================*)
IF (CFG_EN_IOH AND IN_IOH) OR (NOT(CFG_EN_IOH)) OR (SIM) THEN
	STS_IOH := TRUE;
ELSE
	STS_IOH := FALSE;
END_IF 


(*===========================================================================
STATUS - I/O Health
===========================================================================*)
IF (CFG_EN_OOR) AND (IN_OVR OR IN_UNR) THEN
	STS_OOR := TRUE;
ELSE
	STS_OOR := FALSE;
END_IF

(*===========================================================================
STATUS  - CM Ready to Go
CM is Ready to Go if it is Enabled, it's HOA(s) are in the Auto position and Interlocks are met.
NOTE: Not all conditions may exist for a given CM Cla
===========================================================================*)
IF (CFG_EN AND STS_IOH) THEN
	STS_RDY2GO := TRUE;
ELSE
	STS_RDY2GO := FALSE;
END_IF

(*===========================================================================
ALARMS HIHI - Determine HIHI Alarm if Alarms are Enabled.  If an Alarm is already
present when it is Disabled becomes it will stay TRUE.
===========================================================================*)
IF (VAL_ACT > CFG_HIHI_SP) THEN
	WRK_T[1](IN := TRUE);
	IF WRK_T[1].Q THEN
		STS_HIHI := TRUE;
		IF NOT(IN_INH1.0) AND CFG_EN_ALM_HIHI THEN
			ALM_HIHI := TRUE;
		ELSE 
			ALM_HIHI := FALSE;
		END_IF
	ELSE
		STS_HIHI := FALSE;
	END_IF
ELSE 
	STS_HIHI := FALSE;
	ALM_HIHI := FALSE;
	WRK_T[1](IN := FALSE);
END_IF

IF CFG_EN_ALM_HIHI AND ALM_HIHI AND NOT(IN_INH1.0) THEN
	ALMD_HIHI(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_HIHI(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[5].CLK := TRUE;
	 IF WRK_ONS[5].Q THEN
		 ALMD_HIHI.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[5].CLK := FALSE;
END_IF


(*===========================================================================
ALARMS HI - Determine HI Alarm if Alarms are Enabled.  If an Alarm is already
present when it is Disabled becomes it will stay TRUE.
===========================================================================*)
IF (VAL_ACT > CFG_HI_SP) THEN
	WRK_T[2](IN := TRUE);
	IF WRK_T[2].Q THEN
		STS_HI := TRUE;
		IF NOT(IN_INH1.1) AND CFG_EN_ALM_HI THEN
			ALM_HI := TRUE;
		ELSE 
			ALM_HI := FALSE;
		END_IF
	ELSE
		STS_HI := FALSE;
	END_IF
ELSE 
	STS_HI := FALSE;
	ALM_HI := FALSE;
	WRK_T[2](IN := FALSE);
END_IF

IF CFG_EN_ALM_HI AND ALM_HI AND NOT(IN_INH1.1) THEN
	ALMD_HI(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_HI(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[7].CLK := TRUE;
	 IF WRK_ONS[7].Q THEN
		 ALMD_HI.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[7].CLK := FALSE;
END_IF

(*===========================================================================
ALARMS LO - Determine LO Alarm if Alarms are Enabled.  If an Alarm is already
present when it is Disabled becomes it will stay TRUE.
===========================================================================*)
IF (VAL_ACT < CFG_LO_SP) THEN
	WRK_T[3](IN := TRUE);
	IF WRK_T[3].Q THEN
		STS_LO := TRUE;
		IF NOT(IN_INH1.2) AND CFG_EN_ALM_LO THEN
			ALM_LO := TRUE;
		ELSE 
			ALM_LO := FALSE;
		END_IF
	ELSE
		STS_LO := FALSE;
	END_IF
ELSE 
	STS_LO := FALSE;
	ALM_LO := FALSE;
	WRK_T[3](IN := FALSE);
END_IF

IF CFG_EN_ALM_LO AND ALM_LO AND NOT(IN_INH1.2) THEN
	ALMD_LO(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_LO(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[9].CLK := TRUE;
	 IF WRK_ONS[9].Q THEN
		 ALMD_LO.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[9].CLK := FALSE;
END_IF

(*===========================================================================
ALARMS LOLO - Determine LOLO Alarm if Alarms are Enabled.  If an Alarm is already
present when it is Disabled becomes it will stay TRUE.
===========================================================================*)
IF (VAL_ACT < CFG_LOLO_SP) THEN
	WRK_T[4](IN := TRUE);
	IF WRK_T[4].Q THEN
		STS_LOLO := TRUE;
		IF NOT(IN_INH1.3) AND CFG_EN_ALM_LOLO THEN
			ALM_LOLO := TRUE;
		ELSE 
			ALM_LOLO := FALSE;
		END_IF
	ELSE
		STS_LOLO := FALSE;
	END_IF
ELSE 
	STS_LOLO := FALSE;
	ALM_LOLO := FALSE;
	WRK_T[4](IN := FALSE);
END_IF

IF CFG_EN_ALM_LOLO AND ALM_LOLO AND NOT(IN_INH1.3) THEN
	ALMD_LOLO(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_LOLO(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[11].CLK := TRUE;
	 IF WRK_ONS[11].Q THEN
		 ALMD_LOLO.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[11].CLK := FALSE;
END_IF

(*===========================================================================
ALARMS - Out of Range
===========================================================================*)
IF (CFG_EN_ALM_OOR AND STS_OOR) THEN
	WRK_T[5](IN := TRUE);
	IF WRK_T[5].Q THEN
		//STS_OOR := TRUE; SEE LINE 35
		IF NOT(IN_INH1.4) THEN
			ALM_OOR := TRUE;
		ELSE 
			ALM_OOR := FALSE;
		END_IF
	END_IF
ELSE 
	ALM_OOR := FALSE;
	WRK_T[5](IN := FALSE);
END_IF

IF CFG_EN_ALM_OOR AND ALM_OOR AND NOT(IN_INH1.4) THEN
	ALMD_OOR(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_OOR(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[13].CLK := TRUE;
	 IF WRK_ONS[13].Q THEN
		 ALMD_OOR.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[13].CLK := FALSE;
END_IF


(*===========================================================================
ALARMS - FROZEN VALUE
===========================================================================*)
IF (VAL_ACT = CFG_FRZN_SP) THEN
	STS_FRZN := TRUE;
ELSE
	STS_FRZN := FALSE;
END_IF

IF (CFG_EN_ALM_FRZN AND STS_FRZN) THEN
	WRK_T[7](IN := TRUE);
	IF WRK_T[7].Q THEN
		//STS_FRZN := TRUE; SEE LINE 35
		IF NOT(IN_INH1.5)THEN
			ALM_FRZN := TRUE;
		ELSE 
			ALM_FRZN := FALSE;
		END_IF
	END_IF
ELSE 
	ALM_FRZN := FALSE;
	WRK_T[7](IN := FALSE);
END_IF

IF WRK_T[7].ET > T#0S OR CFG_EN_ALM_FRZN THEN
	CFG_FRZN_SP := VAL_ACT;
END_IF

IF CFG_EN_ALM_FRZN AND ALM_FRZN AND NOT(IN_INH1.5) THEN
	ALMD_FRZN(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=60000);
ELSE
	ALMD_FRZN(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[14].CLK := TRUE;
	 IF WRK_ONS[14].Q THEN
		 ALMD_FRZN.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[14].CLK := FALSE;
END_IF

(*===========================================================================
ALARM - I/O Health (Includes Module Health, Channel Calibration Fault, or Channel Fault Alarms)
===========================================================================*)
IF CFG_EN_ALM_IOH AND NOT(STS_IOH) AND NOT(IN_INH1.5) THEN
	ALM_IOH := TRUE;
ELSE
	ALM_IOH := FALSE;
END_IF


IF CFG_EN_ALM_IOH AND ALM_IOH AND NOT(IN_INH1.5) THEN
	ALMD_IOH(IN := TRUE, ACKREQUIRED:=TRUE,LATCHED:=TRUE,MINDURATIONPRE:=1000);
ELSE
	ALMD_IOH(IN := FALSE);
END_IF

IF WRK_B0 THEN
	 WRK_ONS[15].CLK := TRUE;
	 IF WRK_ONS[15].Q THEN
		 ALMD_IOH.OPERACK := TRUE;
	 END_IF
ELSE
	WRK_ONS[15].CLK := FALSE;
END_IF

(*===========================================================================
ALARMS - Any Alarm
===========================================================================*)
IF ALM_HIHI OR ALM_HI OR ALM_LO OR ALM_LOLO OR ALM_OOR OR ALM_IOH OR ALM_FRZN THEN
	ALM_ANY := TRUE;
	STS_ALM_ANY := TRUE;
ELSE
	ALM_ANY := FALSE;
	STS_ALM_ANY := FALSE;
END_IF

(*===========================================================================
ALARMS - Check to see if there are any unacknowledged alarms.
===========================================================================*)
IF (ALM_HIHI AND NOT ALMD_HIHI.ACKED) OR (ALM_HI AND NOT ALMD_HI.ACKED) OR (ALM_LO AND NOT ALMD_LO.ACKED) OR (ALM_LOLO AND NOT ALMD_LOLO.ACKED) OR (ALM_OOR AND NOT ALMD_OOR.ACKED) OR (ALM_IOH AND NOT ALMD_IOH.ACKED) OR (ALM_FRZN AND NOT ALMD_FRZN.ACKED) THEN
	STS_ALM_ANY_UNACK := TRUE;
ELSE
	STS_ALM_ANY_UNACK := FALSE;
END_IF

(*===========================================================================
VALUES - State (HMI Color)
0 = Normal
1 = In Alarm
2 = Disabled
===========================================================================*)
IF NOT ALM_ANY THEN
	VAL_S := 0;
END_IF
IF ALM_ANY AND STS_ALM_ANY_UNACK THEN
	VAL_S := 1;
END_IF
IF ALM_ANY AND NOT STS_ALM_ANY_UNACK THEN
	VAL_S := 3;
END_IF

(*===========================================================================
VALUES - Actual Value
===========================================================================*)
IF CFG_EN_SCL THEN
	// SCALING CONFIGURATION
	IF CFG_SCL_TYP <> 1 AND CFG_SCL_TYP <> 2 THEN
		CFG_SCL_TYP := 1;
	END_IF
	IF CFG_RU_MAX = 0 AND CFG_RU_MIN = 0 THEN
		CFG_RU_MAX := 20;
		CFG_RU_MIN := 4;
	END_IF
	IF CFG_EU_MAX = 0 THEN
		CFG_EU_MAX := 1;
	END_IF
	IF CFG_RU_MAX = 0 THEN
		CFG_RU_MAX := 1;
	END_IF
	// LINEAR SCALING
	IF CFG_SCL_TYP = 1 THEN
		IF CFG_RU_MAX > CFG_RU_MIN THEN
			WRK_R[4] := (IN_RAW - DINT_TO_REAL(CFG_RU_MIN))/DINT_TO_REAL(CFG_RU_MAX - CFG_RU_MIN) * (CFG_EU_MAX-CFG_EU_MIN)+CFG_EU_MIN;
		END_IF
	END_IF
	// NON-LINEAR SCALING CONFIGURATION
	IF CFG_SCL_TYP = 2 THEN
		IF CFG_EU_MIN >= 0 THEN
			WRK_R[1] := LREAL_TO_REAL(EXPT(CFG_EU_MIN, 2));
		END_IF
		IF CFG_EU_MIN < 0 THEN
			WRK_R[1] := LREAL_TO_REAL(-1.0*EXPT(CFG_EU_MIN, 2));
		END_IF
		IF CFG_EU_MAX >= 0 THEN
			WRK_R[2] := LREAL_TO_REAL(EXPT(CFG_EU_MAX, 2));
		END_IF
		IF CFG_EU_MAX < 0 THEN
			WRK_R[2] := LREAL_TO_REAL(-1.0*EXPT(CFG_EU_MAX, 2));
		END_IF
		// NON-LINEAR SCALING 
		IF CFG_RU_MAX > CFG_RU_MIN THEN
			WRK_R[3] := (IN_RAW - DINT_TO_REAL(CFG_RU_MIN))* (WRK_R[2] - WRK_R[1])/ DINT_TO_REAL(CFG_RU_MAX-CFG_RU_MIN)+ WRK_R[1];
		END_IF
		IF WRK_R[3] >= 0 THEN
			WRK_R[4] := SQRT(WRK_R[3]);
		END_IF	
		IF WRK_R[3] < 0 THEN
			WRK_R[4] := -1.0 * SQRT(WRK_R[3]);
		END_IF
	END_IF
END_IF

IF NOT CFG_EN_SCL THEN
	WRK_R[4] := IN_RAW;
END_IF

VAL_ACT := WRK_R[4];

IF SIM THEN
	VAL_ACT := WRK_R[5];
END_IF

(*===========================================================================
SIM - Don't let the SIM bit stay on for more than 24 hours.
ADDED - TIMER BYPASS: NOTE SIM IS FOREVER!!!!
===========================================================================*)
IF SIM AND NOT CFG_TMR_DISABLE_SIM THEN
	WRK_T[6](IN := TRUE, PT := T#1440000MS);
	IF WRK_T[6].Q THEN
		SIM := FALSE;
	END_IF 
ELSE
	WRK_T[6](IN := FALSE);
END_IF

(*===========================================================================
Unlatch Alarms Reset Bit
===========================================================================*)
IF WRK_B0 THEN
	WRK_B0 := FALSE;
END_IF

(*===========================================================================
CM DISABLED - Set Conditions Upon Entering Disabled State
===========================================================================*)
IF WRK_B1 THEN
	WRK_B0 := FALSE;
	VAL_S := 3;
	SIM := FALSE;
END_IF

(*===========================================================================
Exit AOI
===========================================================================*)
EXIT_AOI_CM_AIN:

END_FUNCTION_BLOCK
