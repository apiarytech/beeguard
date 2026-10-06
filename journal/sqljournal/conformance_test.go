/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package sqljournal

import (
	"testing"

	"github.com/apiarytech/beeguard/journal/journaltest"
)

func TestConformance(t *testing.T) {
	j, _ := open(t)
	journaltest.Run(t, j)
}
