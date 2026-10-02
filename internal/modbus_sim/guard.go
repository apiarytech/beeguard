/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package modbussim

import (
	"math"
	"time"
)

// Wire addresses of the simulated guard. In PLC4X notation they are
// holding-register:1:REAL, holding-register:3:REAL, holding-register:5:REAL and coil:1:BOOL.
const (
	TempAddr     = 0 // brood temperature, °C
	WeightAddr   = 2 // guard weight, kg
	HumidityAddr = 4 // relative humidity, %
	LidAddr      = 0 // lid open coil
)

// Guard is a simulated guarded device whose readings cycle through alarm conditions:
//   - the temperature swings 31.5..39.5 °C every 2 minutes, crossing a 38 °C limit;
//   - the weight drops 2.5 kg in 5 s at 90 s of every 3 minutes (a swarm) and
//     recovers at the start of the next cycle;
//   - the lid is open from 30 s to 40 s of every minute.
type Guard struct{}

// Update writes the guard's readings at elapsed time since the start.
func (Guard) Update(s *Server, elapsed time.Duration) {
	t := elapsed.Seconds()
	s.SetFloat32(TempAddr, float32(35.5+4*math.Sin(2*math.Pi*t/120)))
	s.SetFloat32(HumidityAddr, float32(60+5*math.Sin(t/40)))

	weight := 42.0
	if phase := math.Mod(t, 180); phase >= 90 {
		weight -= 2.5 * math.Min((phase-90)/5, 1)
	}
	s.SetFloat32(WeightAddr, float32(weight))

	phase := math.Mod(t, 60)
	s.SetCoil(LidAddr, phase >= 30 && phase < 40)
}
