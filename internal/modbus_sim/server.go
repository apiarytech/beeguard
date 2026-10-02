/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package modbussim is a minimal Modbus TCP server for demonstrations and
// tests. It serves coils, discrete inputs, holding registers and input
// registers (function codes 1-6, 15 and 16) from memory. Addresses are the
// zero-based wire addresses; PLC4X's "holding-register:1" is wire address 0.
package modbussim

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"sync"
)

// Modbus exception codes.
const (
	illegalFunction = 1
	illegalAddress  = 2
	illegalValue    = 3
)

// Server holds the four Modbus data areas. It is safe for concurrent use.
type Server struct {
	mu       sync.Mutex
	coils    []bool
	discrete []bool
	holding  []uint16
	input    []uint16
}

// New returns a server with size entries in each area.
func New(size int) *Server {
	return &Server{
		coils:    make([]bool, size),
		discrete: make([]bool, size),
		holding:  make([]uint16, size),
		input:    make([]uint16, size),
	}
}

// SetFloat32 stores v in holding registers addr and addr+1, high word first
// (big-endian, the PLC4X default byte order).
func (s *Server) SetFloat32(addr int, v float32) {
	bits := math.Float32bits(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holding[addr] = uint16(bits >> 16)
	s.holding[addr+1] = uint16(bits)
}

// Float32 reads the value SetFloat32 stores.
func (s *Server) Float32(addr int) float32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return math.Float32frombits(uint32(s.holding[addr])<<16 | uint32(s.holding[addr+1]))
}

// SetCoil sets a coil.
func (s *Server) SetCoil(addr int, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.coils[addr] = v
}

// Coil reads a coil.
func (s *Server) Coil(addr int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.coils[addr]
}

// Serve accepts connections on ln until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		wg.Go(func() {
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			s.serveConn(conn)
		})
	}
}

// serveConn answers requests until the connection closes. Each frame is an
// MBAP header (transaction, protocol, length, unit) followed by the PDU.
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	header := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if length < 2 || length > 260 {
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		resp := s.Handle(pdu)
		frame := make([]byte, 7, 7+len(resp))
		copy(frame, header[:4])
		binary.BigEndian.PutUint16(frame[4:6], uint16(len(resp)+1))
		frame[6] = header[6]
		if _, err := conn.Write(append(frame, resp...)); err != nil {
			return
		}
	}
}

// Handle answers one request PDU (function code and data).
func (s *Server) Handle(pdu []byte) []byte {
	if len(pdu) == 0 {
		return []byte{0x80, illegalFunction}
	}
	fc := pdu[0]
	if len(pdu) < 5 { // every supported function has at least an address and a quantity or value
		return []byte{fc | 0x80, illegalValue}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var resp []byte
	code := byte(0)
	switch fc {
	case 1:
		resp, code = readBits(fc, s.coils, pdu)
	case 2:
		resp, code = readBits(fc, s.discrete, pdu)
	case 3:
		resp, code = readRegisters(fc, s.holding, pdu)
	case 4:
		resp, code = readRegisters(fc, s.input, pdu)
	case 5:
		resp, code = s.writeCoil(pdu)
	case 6:
		resp, code = s.writeRegister(pdu)
	case 15:
		resp, code = s.writeCoils(pdu)
	case 16:
		resp, code = s.writeRegisters(pdu)
	default:
		code = illegalFunction
	}
	if code != 0 {
		return []byte{fc | 0x80, code}
	}
	return resp
}

func span(pdu []byte, size, maxQty int) (start, qty int, code byte) {
	start = int(binary.BigEndian.Uint16(pdu[1:3]))
	qty = int(binary.BigEndian.Uint16(pdu[3:5]))
	if qty < 1 || qty > maxQty {
		return 0, 0, illegalValue
	}
	if start+qty > size {
		return 0, 0, illegalAddress
	}
	return start, qty, 0
}

func readBits(fc byte, area []bool, pdu []byte) ([]byte, byte) {
	start, qty, code := span(pdu, len(area), 2000)
	if code != 0 {
		return nil, code
	}
	n := (qty + 7) / 8
	resp := make([]byte, 2+n)
	resp[0], resp[1] = fc, byte(n)
	for i := range qty {
		if area[start+i] {
			resp[2+i/8] |= 1 << (i % 8)
		}
	}
	return resp, 0
}

func readRegisters(fc byte, area []uint16, pdu []byte) ([]byte, byte) {
	start, qty, code := span(pdu, len(area), 125)
	if code != 0 {
		return nil, code
	}
	resp := make([]byte, 2+2*qty)
	resp[0], resp[1] = fc, byte(2*qty)
	for i := range qty {
		binary.BigEndian.PutUint16(resp[2+2*i:], area[start+i])
	}
	return resp, 0
}

func (s *Server) writeCoil(pdu []byte) ([]byte, byte) {
	addr := int(binary.BigEndian.Uint16(pdu[1:3]))
	value := binary.BigEndian.Uint16(pdu[3:5])
	if addr >= len(s.coils) {
		return nil, illegalAddress
	}
	if value != 0xFF00 && value != 0 {
		return nil, illegalValue
	}
	s.coils[addr] = value == 0xFF00
	return pdu[:5], 0
}

func (s *Server) writeRegister(pdu []byte) ([]byte, byte) {
	addr := int(binary.BigEndian.Uint16(pdu[1:3]))
	if addr >= len(s.holding) {
		return nil, illegalAddress
	}
	s.holding[addr] = binary.BigEndian.Uint16(pdu[3:5])
	return pdu[:5], 0
}

func (s *Server) writeCoils(pdu []byte) ([]byte, byte) {
	start, qty, code := span(pdu, len(s.coils), 1968)
	if code != 0 {
		return nil, code
	}
	if len(pdu) < 6 || int(pdu[5]) != (qty+7)/8 || len(pdu) < 6+int(pdu[5]) {
		return nil, illegalValue
	}
	for i := range qty {
		s.coils[start+i] = pdu[6+i/8]&(1<<(i%8)) != 0
	}
	return pdu[:5], 0
}

func (s *Server) writeRegisters(pdu []byte) ([]byte, byte) {
	start, qty, code := span(pdu, len(s.holding), 123)
	if code != 0 {
		return nil, code
	}
	if len(pdu) < 6 || int(pdu[5]) != 2*qty || len(pdu) < 6+2*qty {
		return nil, illegalValue
	}
	for i := range qty {
		s.holding[start+i] = binary.BigEndian.Uint16(pdu[6+2*i:])
	}
	return pdu[:5], 0
}
