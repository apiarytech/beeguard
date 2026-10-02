/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Command modbus_sim serves a simulated guard over Modbus TCP, for trying
// beeguard without field devices. See examples/guard.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/apiarytech/beeguard/internal/modbus_sim"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5020", "listen address")
	interval := flag.Duration("interval", 100*time.Millisecond, "how often the guard readings change")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	server := modbussim.New(64)
	guard := modbussim.Guard{}
	start := time.Now()
	guard.Update(server, 0)
	go func() {
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				guard.Update(server, time.Since(start))
			}
		}
	}()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("simulated guard on modbus-tcp://%s (Ctrl+C to stop)", ln.Addr())
	if err := server.Serve(ctx, ln); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
