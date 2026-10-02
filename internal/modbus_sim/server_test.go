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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestHandle(t *testing.T) {
	s := New(16)
	s.SetFloat32(2, 38.5)
	s.SetCoil(1, true)
	s.SetCoil(9, true)
	cases := []struct {
		name string
		req  []byte
		want []byte
	}{
		{"read holding", []byte{3, 0, 2, 0, 2}, []byte{3, 4, 0x42, 0x1A, 0x00, 0x00}},
		{"read input", []byte{4, 0, 0, 0, 1}, []byte{4, 2, 0, 0}},
		{"read coils", []byte{1, 0, 0, 0, 10}, []byte{1, 2, 0x02, 0x02}},
		{"read discrete", []byte{2, 0, 0, 0, 1}, []byte{2, 1, 0}},
		{"write coil", []byte{5, 0, 3, 0xFF, 0}, []byte{5, 0, 3, 0xFF, 0}},
		{"write register", []byte{6, 0, 7, 0x12, 0x34}, []byte{6, 0, 7, 0x12, 0x34}},
		{"write coils", []byte{15, 0, 4, 0, 3, 1, 0x05}, []byte{15, 0, 4, 0, 3}},
		{"write registers", []byte{16, 0, 8, 0, 2, 4, 0, 1, 0, 2}, []byte{16, 0, 8, 0, 2}},
		{"bad function", []byte{43, 0, 0, 0, 1}, []byte{43 | 0x80, illegalFunction}},
		{"bad address", []byte{3, 0, 15, 0, 2}, []byte{3 | 0x80, illegalAddress}},
		{"bad quantity", []byte{3, 0, 0, 0, 0}, []byte{3 | 0x80, illegalValue}},
		{"bad coil value", []byte{5, 0, 0, 0x12, 0x34}, []byte{5 | 0x80, illegalValue}},
		{"short", []byte{3, 0}, []byte{3 | 0x80, illegalValue}},
		{"empty", nil, []byte{0x80, illegalFunction}},
		{"bad byte count", []byte{16, 0, 0, 0, 2, 3, 0, 1, 0}, []byte{16 | 0x80, illegalValue}},
	}
	for _, c := range cases {
		if got := s.Handle(c.req); !bytes.Equal(got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, got, c.want)
		}
	}
	if !s.Coil(3) || !s.Coil(4) || s.Coil(5) || !s.Coil(6) {
		t.Error("write coils")
	}
	if s.holding[7] != 0x1234 || s.holding[8] != 1 || s.holding[9] != 2 {
		t.Error("write registers")
	}
	if s.Float32(2) != 38.5 {
		t.Error("float round trip")
	}
}

func TestServeTCP(t *testing.T) {
	s := New(8)
	s.SetFloat32(0, 21.25)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Serve(ctx, ln) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Transaction 7, protocol 0, length 6, unit 1, read holding registers 0..1.
	if _, err := conn.Write([]byte{0, 7, 0, 0, 0, 6, 1, 3, 0, 0, 0, 2}); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 13)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(resp[0:2]) != 7 || resp[6] != 1 || resp[7] != 3 || resp[8] != 4 {
		t.Fatalf("response % x", resp)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve = %v", err)
	}
}

func TestGuard(t *testing.T) {
	s := New(8)
	g := Guard{}
	g.Update(s, 30*time.Second) // temperature peak, lid open
	if got := s.Float32(TempAddr); got < 39 || got > 39.6 {
		t.Errorf("temperature at peak = %v", got)
	}
	if !s.Coil(LidAddr) {
		t.Error("lid closed at 30 s")
	}
	g.Update(s, 100*time.Second) // after the swarm
	if got := s.Float32(WeightAddr); got != 39.5 {
		t.Errorf("weight after the swarm = %v", got)
	}
	if s.Coil(LidAddr) {
		t.Error("lid open at 100 s")
	}
	g.Update(s, 180*time.Second)
	if got := s.Float32(WeightAddr); got != 42 {
		t.Errorf("weight after recovery = %v", got)
	}
}
