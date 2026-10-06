/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package journal_test

import (
	"testing"

	"github.com/apiarytech/beeguard/journal"
	"github.com/apiarytech/beeguard/journal/journaltest"
)

func TestMemoryConformance(t *testing.T) { journaltest.Run(t, journal.NewMemory(0)) }
