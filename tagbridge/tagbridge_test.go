/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package tagbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apiarytech/honeycomb"
	plc "github.com/apiarytech/royaljelly/iec"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
	"github.com/apiarytech/beeguard/journal"
)

type fixture struct {
	db      *honeycomb.TagDatabase
	eng     *engine.Engine
	bridge  *Bridge
	journal *journal.Memory
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db := honeycomb.NewTagDatabase()
	for _, tag := range []*honeycomb.Tag{
		{Name: "Guard1.Temp", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeREAL}, Value: plc.REAL(30)},
		{Name: "Guard1.Lid", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeBOOL}, Value: plc.BOOL(false)},
	} {
		if err := db.AddTag(tag); err != nil {
			t.Fatal(err)
		}
	}
	defs := []engine.Definition{
		{
			Alarm:     alarm.Config{ID: "Guard1.TempHigh", Severity: 700, AckRequired: true, MaxShelve: time.Hour},
			Source:    "Guard1.Temp",
			Condition: evaluator.Config{Kind: evaluator.High, Limit: 38, Deadband: 1},
		},
		{
			Alarm:     alarm.Config{ID: "Guard1.LidOpen", Severity: 300, AckRequired: true, Latched: true},
			Source:    "Guard1.Lid",
			Condition: evaluator.Config{Kind: evaluator.Digital},
		},
	}
	f := &fixture{db: db, journal: journal.NewMemory(0)}
	f.bridge = New(db, nil, WithErrorHandler(func(err error) { t.Error(err) }))
	eng, err := engine.New(defs, engine.WithSink(f.journal), engine.WithSink(f.bridge),
		engine.WithErrorHandler(func(err error) { t.Error(err) }))
	if err != nil {
		t.Fatal(err)
	}
	f.eng = eng
	f.bridge.SetEngine(eng)
	if err := f.bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- f.bridge.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v", err)
		}
	})
	return f
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *fixture) status(t *testing.T, id string) *AlarmStatus {
	t.Helper()
	v, err := f.db.GetTagValue(f.bridge.StatusTag(id))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*AlarmStatus)
}

func (f *fixture) set(t *testing.T, name string, value any) {
	t.Helper()
	if err := f.db.SetTagValue(name, value); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) result(t *testing.T, id string) string {
	t.Helper()
	v, err := f.db.GetTagValue(f.bridge.CommandTag(id) + ".Result")
	if err != nil {
		t.Fatal(err)
	}
	return string(v.(plc.STRING))
}

func TestTagNames(t *testing.T) {
	b := New(nil, nil, WithPrefix("A_"))
	if got := b.StatusTag("Guard 1.Temp-High"); got != "A_Guard_1_Temp_High" {
		t.Fatalf("StatusTag = %q", got)
	}
	if got := b.CommandTag("x.y"); got != "A_x_y_CMD" {
		t.Fatalf("CommandTag = %q", got)
	}
}

func TestSourceUpdatesDriveStatusTag(t *testing.T) {
	f := setup(t)
	if _, ok := f.db.GetTag("ALM_Guard1_TempHigh_CMD"); !ok {
		t.Fatal("command tag not created")
	}
	f.set(t, "Guard1.Temp", plc.REAL(39))
	eventually(t, "the alarm to activate", func() bool { return bool(f.status(t, "Guard1.TempHigh").InAlarm) })
	s := f.status(t, "Guard1.TempHigh")
	if s.State != plc.DINT(alarm.ActiveUnacked) || !bool(s.Unacked) || s.Priority != plc.DINT(alarm.High) ||
		s.AlarmCount != 1 || time.Time(s.InAlarmTime).IsZero() {
		t.Fatalf("status = %+v", s)
	}
}

func TestCommandTag(t *testing.T) {
	f := setup(t)
	cmd := f.bridge.CommandTag("Guard1.TempHigh")
	f.set(t, "Guard1.Temp", plc.REAL(39))
	eventually(t, "activation", func() bool { return bool(f.status(t, "Guard1.TempHigh").InAlarm) })

	f.set(t, cmd+".User", plc.STRING("franklin"))
	f.set(t, cmd+".Ack", plc.BOOL(true))
	eventually(t, "the ack", func() bool { return bool(f.status(t, "Guard1.TempHigh").Acked) })
	eventually(t, "the result", func() bool { return f.result(t, "Guard1.TempHigh") == "OK" })
	if v, _ := f.db.GetTagValue(cmd + ".Ack"); v != plc.BOOL(false) {
		t.Fatal("the bridge did not clear Ack")
	}
	acks, _ := f.journal.Query(context.Background(), journal.Filter{Kinds: []alarm.EventKind{alarm.Acknowledged}})
	if len(acks) != 1 || acks[0].User != "franklin" {
		t.Fatalf("journal = %+v", acks)
	}

	// A second ack fails, and the reason is written to Result.
	f.set(t, cmd+".Ack", plc.BOOL(true))
	eventually(t, "the error", func() bool { return strings.Contains(f.result(t, "Guard1.TempHigh"), "nothing to acknowledge") })

	f.set(t, cmd+".ShelveMinutes", plc.DINT(15))
	f.set(t, cmd+".Shelve", plc.BOOL(true))
	eventually(t, "the shelve", func() bool { return bool(f.status(t, "Guard1.TempHigh").Shelved) })
	s, _ := f.eng.Status("Guard1.TempHigh")
	if got := s.ShelveExpiry.Sub(s.ShelveTime); got != 15*time.Minute {
		t.Fatalf("shelved for %v", got)
	}
}

func TestAckAndResetTogether(t *testing.T) {
	f := setup(t)
	cmd := f.bridge.CommandTag("Guard1.LidOpen")
	f.set(t, "Guard1.Lid", plc.BOOL(true))
	// Wait: the subscription keeps only the latest update, so an immediate
	// FALSE could replace TRUE before the bridge sees it.
	eventually(t, "activation", func() bool { return bool(f.status(t, "Guard1.LidOpen").InAlarm) })
	f.set(t, "Guard1.Lid", plc.BOOL(false))
	eventually(t, "latched", func() bool {
		return f.status(t, "Guard1.LidOpen").State == plc.DINT(alarm.LatchedUnacked)
	})
	// The HMI writes the whole command at once; Ack runs before Reset.
	f.set(t, cmd, &AlarmCommand{Ack: true, Reset: true})
	eventually(t, "reset", func() bool { return f.status(t, "Guard1.LidOpen").State == plc.DINT(alarm.Normal) })
}

func TestBadQualityHoldsAlarm(t *testing.T) {
	f := setup(t)
	f.set(t, "Guard1.Temp", plc.REAL(39))
	eventually(t, "activation", func() bool { return bool(f.status(t, "Guard1.TempHigh").InAlarm) })
	if err := f.db.SetTagValueQuality("Guard1.Temp", plc.REAL(0), honeycomb.QualityBad); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if !bool(f.status(t, "Guard1.TempHigh").InAlarm) {
		t.Fatal("a bad-quality value cleared the alarm")
	}
}

func TestToSample(t *testing.T) {
	ts := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		r    honeycomb.Reading
		want evaluator.Sample
	}{
		{honeycomb.Reading{Value: plc.BOOL(true), Quality: honeycomb.QualityGood, Timestamp: ts}, evaluator.Sample{Value: 1, Good: true, Time: ts}},
		{honeycomb.Reading{Value: plc.DINT(-7), Quality: honeycomb.QualityUncertain}, evaluator.Sample{Value: -7, Good: true}},
		{honeycomb.Reading{Value: plc.UDINT(7), Quality: honeycomb.QualityBad}, evaluator.Sample{Value: 7}},
		{honeycomb.Reading{Value: plc.LREAL(2.5), Quality: honeycomb.QualityUnknown}, evaluator.Sample{Value: 2.5}},
		// Values read over the network arrive as JSON numbers and booleans.
		{honeycomb.Reading{Value: 38.5, Quality: honeycomb.QualityGood}, evaluator.Sample{Value: 38.5, Good: true}},
		{honeycomb.Reading{Value: true, Quality: honeycomb.QualityGood}, evaluator.Sample{Value: 1, Good: true}},
	}
	for i := range cases {
		got, err := ToSample(cases[i].r)
		if err != nil || got != cases[i].want {
			t.Errorf("case %d: got %+v, %v; want %+v", i, got, err, cases[i].want)
		}
	}
	if s, err := ToSample(honeycomb.Reading{Value: plc.STRING("x"), Quality: honeycomb.QualityGood}); err == nil || s.Good {
		t.Fatal("accepted a STRING")
	}
}

// remoteFixture links a local database to a "field" database the way linked
// mode does, with the alarm source as a remote alias that the bridge polls.
func remoteFixture(t *testing.T, field honeycomb.DatabaseAccessor, defs []engine.Definition) (*honeycomb.TagDatabase, *engine.Engine, func() []error) {
	t.Helper()
	local := honeycomb.NewTagDatabase()
	if err := local.RegisterDatabase("field", field); err != nil {
		t.Fatal(err)
	}
	if err := local.AddTag(&honeycomb.Tag{Name: "Guard1.Temp", RemoteAlias: &honeycomb.RemoteAliasInfo{DBID: "field", TagName: "Guard1.Temp"}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var errs []error
	b := New(local, nil, WithPollInterval(10*time.Millisecond), WithErrorHandler(func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}))
	eng, err := engine.New(defs, engine.WithSink(b))
	if err != nil {
		t.Fatal(err)
	}
	b.SetEngine(eng)
	if err := b.Setup(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return local, eng, func() []error {
		mu.Lock()
		defer mu.Unlock()
		return append([]error(nil), errs...)
	}
}

func TestPolledRemoteAlias(t *testing.T) {
	field := honeycomb.NewTagDatabase()
	if err := field.AddTag(&honeycomb.Tag{Name: "Guard1.Temp", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeREAL}, Value: plc.REAL(30)}); err != nil {
		t.Fatal(err)
	}
	_, eng, _ := remoteFixture(t, field, []engine.Definition{{
		Alarm:     alarm.Config{ID: "Guard1.TempHigh", Severity: 700, AckRequired: true},
		Source:    "Guard1.Temp",
		Condition: evaluator.Config{Kind: evaluator.High, Limit: 38},
	}})
	stamp := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	if err := field.SetTagValueQualityAt("Guard1.Temp", plc.REAL(39), honeycomb.QualityGood, stamp); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the polled value to raise the alarm", func() bool {
		s, _ := eng.Status("Guard1.TempHigh")
		return s.InAlarm()
	})
}

func TestUnreachableRemoteIsBadQuality(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close() // nothing listens any more
	client := &honeycomb.NetworkDatabaseClient{RemoteAddress: server.URL, Client: server.Client()}
	_, eng, _ := remoteFixture(t, client, []engine.Definition{{
		Alarm:     alarm.Config{ID: "Guard1.CommFail", Severity: 800, AckRequired: true},
		Source:    "Guard1.Temp",
		Condition: evaluator.Config{Kind: evaluator.BadQuality},
	}})
	eventually(t, "the bad-quality alarm", func() bool {
		s, _ := eng.Status("Guard1.CommFail")
		return s.InAlarm()
	})
}

func TestFailingSourceReportedOnce(t *testing.T) {
	var got []error
	b := New(honeycomb.NewTagDatabase(), nil, WithErrorHandler(func(err error) { got = append(got, err) }))
	boom := errors.New("down")
	b.reportOnce("S", boom)
	b.reportOnce("S", boom)
	b.reportOnce("S", nil)
	b.reportOnce("S", boom)
	if len(got) != 2 {
		t.Fatalf("reported %d times, want 2 (once per failure period)", len(got))
	}
}

func TestSetupNeedsSourceTags(t *testing.T) {
	eng, err := engine.New([]engine.Definition{{
		Alarm:     alarm.Config{ID: "X", Severity: 500},
		Source:    "Missing",
		Condition: evaluator.Config{Kind: evaluator.Digital},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := New(honeycomb.NewTagDatabase(), eng).Setup(); err == nil {
		t.Fatal("Setup accepted a missing source tag")
	}
}

// Many writers and HMI commands at once must not deadlock.
func TestConcurrentWritesAndCommands(t *testing.T) {
	f := setup(t)
	cmd := f.bridge.CommandTag("Guard1.TempHigh")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			f.set(t, "Guard1.Temp", plc.REAL(30+float32(i%2)*10))
			f.set(t, cmd+".Ack", plc.BOOL(true))
		})
	}
	wg.Wait()
	f.set(t, "Guard1.Temp", plc.REAL(30))
	eventually(t, "return to normal", func() bool { return !bool(f.status(t, "Guard1.TempHigh").Condition) })
}

func lidDefs() []engine.Definition {
	return []engine.Definition{{
		Alarm:     alarm.Config{ID: "Guard1.LidOpen", Severity: 400, AckRequired: true},
		Source:    "Guard1.Temp", // the fixture's linked tag; used here as a BOOL
		Condition: evaluator.Config{Kind: evaluator.Digital},
	}}
}

// A pulse far shorter than the poll interval is caught through the change feed.
func TestFeedCatchesShortPulse(t *testing.T) {
	field := honeycomb.NewTagDatabase()
	if err := field.AddTag(&honeycomb.Tag{Name: "Guard1.Temp", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeBOOL}, Value: plc.BOOL(false)}); err != nil {
		t.Fatal(err)
	}
	_, eng, _ := remoteFixture(t, field, lidDefs())
	time.Sleep(50 * time.Millisecond) // the bridge has started following the feed
	t0 := time.Now()
	_ = field.SetTagValueQualityAt("Guard1.Temp", plc.BOOL(true), honeycomb.QualityGood, t0)
	_ = field.SetTagValueQualityAt("Guard1.Temp", plc.BOOL(false), honeycomb.QualityGood, t0.Add(time.Millisecond))
	eventually(t, "the pulse to raise and clear the alarm", func() bool {
		s, _ := eng.Status("Guard1.LidOpen")
		return s.State == alarm.RTNUnacked && s.Count == 1
	})
}

// A server without a change feed is polled instead.
func TestFallbackToPollingWithoutFeed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tags/Guard1.Temp", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value": true, "quality": 1}`))
	})
	old := httptest.NewServer(mux) // no /changes: an older honeycomb
	defer old.Close()
	client := &honeycomb.NetworkDatabaseClient{RemoteAddress: old.URL, Client: old.Client()}
	_, eng, errs := remoteFixture(t, client, lidDefs())
	eventually(t, "the polled value to raise the alarm", func() bool {
		s, _ := eng.Status("Guard1.LidOpen")
		return s.InAlarm()
	})
	found := false
	for _, err := range errs() {
		found = found || strings.Contains(err.Error(), "has no change feed")
	}
	if !found {
		t.Fatalf("the fallback was not reported: %v", errs())
	}
}

// fakeField serves a change feed and, optionally, a batch read, and counts the
// requests, as an older or newer honeycomb would.
type fakeField struct {
	mu                   sync.Mutex
	batchReads, tagReads int
}

func (f *fakeField) count(n *int) {
	f.mu.Lock()
	*n++
	f.mu.Unlock()
}

func (f *fakeField) counts() (batch, single int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batchReads, f.tagReads
}

func (f *fakeField) server(t *testing.T, batch bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /changes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Epoch  string `json:"epoch"`
			WaitMS int64  `json:"wait_ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Epoch != "" { // an established reader: nothing changes
			select {
			case <-r.Context().Done():
			case <-time.After(time.Duration(req.WaitMS) * time.Millisecond):
			}
		}
		_, _ = w.Write([]byte(`{"epoch": "e1", "next": 0, "gap": false, "changes": []}`))
	})
	if batch {
		mux.HandleFunc("POST /tags", func(w http.ResponseWriter, _ *http.Request) {
			f.count(&f.batchReads)
			_, _ = w.Write([]byte(`{"tags": {"Guard1.Temp": {"value": true, "quality": 1}}}`))
		})
	}
	mux.HandleFunc("GET /tags/Guard1.Temp", func(w http.ResponseWriter, _ *http.Request) {
		f.count(&f.tagReads)
		_, _ = w.Write([]byte(`{"value": true, "quality": 1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Resync reads current values with one batch request, and falls back to one
// request per tag on a server without the batch read.
func TestResyncUsesBatchRead(t *testing.T) {
	for _, batch := range []bool{true, false} {
		t.Run(fmt.Sprintf("batch=%v", batch), func(t *testing.T) {
			field := &fakeField{}
			srv := field.server(t, batch)
			client := &honeycomb.NetworkDatabaseClient{RemoteAddress: srv.URL, Client: srv.Client()}
			_, eng, _ := remoteFixture(t, client, lidDefs())
			eventually(t, "the resync to raise the alarm", func() bool {
				s, _ := eng.Status("Guard1.LidOpen")
				return s.InAlarm()
			})
			batchReads, tagReads := field.counts()
			if batch && (batchReads != 1 || tagReads != 0) {
				t.Fatalf("batch server: %d batch reads, %d tag reads; want 1 and 0", batchReads, tagReads)
			}
			if !batch && tagReads != 1 {
				t.Fatalf("older server: %d tag reads, want 1", tagReads)
			}
		})
	}
}
