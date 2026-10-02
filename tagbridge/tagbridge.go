/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package tagbridge connects an alarm engine to a honeycomb TagDatabase.
//
//   - Every alarm source is a tag. The bridge subscribes to a local tag; a
//     remote alias (a tag linked to another honeycomb database) cannot be
//     subscribed to, so it is polled every poll interval. Each value goes to the
//     engine with its quality and timestamp. BOOL and numeric values are
//     supported, Good or Uncertain quality counts as good, and a source that
//     cannot be read counts as bad quality.
//   - Every alarm gets a status tag, an AlarmStatus UDT the bridge rewrites on
//     each of the alarm's events, so HMIs and PLC logic can read it.
//   - Every alarm gets a command tag, an AlarmCommand UDT. An HMI sets a
//     command field (e.g. ALM_Guard1_TempHigh_CMD.Ack := TRUE); the bridge runs
//     the command, clears the field and writes the outcome to Result. This is
//     the CMD_OP_* pattern of the BG_ALM_DIG function block.
//
// Tag names are the prefix plus the alarm ID with every character other than
// letters, digits and '_' replaced by '_', because honeycomb reads the first
// '.' in a name as a UDT field separator.
package tagbridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/apiarytech/honeycomb"
	plc "github.com/apiarytech/royaljelly/iec"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
)

// AlarmStatus is the UDT of a status tag.
type AlarmStatus struct {
	State        plc.DINT // alarm.State: 0 NORMAL .. 8 OUT_OF_SERVICE
	InAlarm      plc.BOOL
	Acked        plc.BOOL
	InAlarmUnack plc.BOOL
	Unacked      plc.BOOL // any acknowledgement pending (horn)
	Condition    plc.BOOL
	Shelved      plc.BOOL
	Suppressed   plc.BOOL
	Disabled     plc.BOOL
	Chattering   plc.BOOL
	Priority     plc.DINT // 1 urgent .. 4 low
	Severity     plc.DINT
	AlarmCount   plc.DINT
	InAlarmTime  plc.DT
	AckTime      plc.DT
	RTNTime      plc.DT
	ShelveExpiry plc.DT
}

// TypeName implements honeycomb.UDT.
func (*AlarmStatus) TypeName() honeycomb.DataType { return "BeeguardAlarmStatus" }

// AlarmCommand is the UDT of a command tag. The BOOL fields are one-shot
// requests the bridge clears; with several set at once they run in field order,
// so Enable, Unsuppress and Unshelve win over their opposites, and Ack runs
// before Reset.
type AlarmCommand struct {
	Disable       plc.BOOL
	Enable        plc.BOOL
	Suppress      plc.BOOL
	Unsuppress    plc.BOOL
	Shelve        plc.BOOL
	Unshelve      plc.BOOL
	Ack           plc.BOOL
	Reset         plc.BOOL
	ResetCount    plc.BOOL
	ShelveMinutes plc.DINT   // duration of the next Shelve; 0 = the alarm's maximum
	OneShot       plc.BOOL   // the next Shelve ends when the condition returns to normal
	User          plc.STRING // recorded in the journal; empty = the bridge's default user
	Result        plc.STRING // written by the bridge: "OK" or why the last command failed
}

// TypeName implements honeycomb.UDT.
func (*AlarmCommand) TypeName() honeycomb.DataType { return "BeeguardAlarmCommand" }

// Option configures a Bridge.
type Option func(*Bridge)

// WithPrefix sets the prefix of status and command tag names. The default is "ALM_".
func WithPrefix(prefix string) Option { return func(b *Bridge) { b.prefix = prefix } }

// WithUser sets the user recorded for commands whose User field is empty. The default is "hmi".
func WithUser(user string) Option { return func(b *Bridge) { b.user = user } }

// WithErrorHandler receives errors the bridge cannot return, e.g. a source
// value that is not a number. The default drops them.
func WithErrorHandler(fn func(error)) Option { return func(b *Bridge) { b.onError = fn } }

// WithPollInterval sets how often linked sources are read from a database
// without a change feed, and how long to wait before retrying a failed feed.
// The default is one second.
func WithPollInterval(d time.Duration) Option { return func(b *Bridge) { b.pollEvery = d } }

// WithFeedWait sets how long one change-feed request waits for changes
// before it is renewed. The default is 25 seconds.
func WithFeedWait(d time.Duration) Option { return func(b *Bridge) { b.feedWait = d } }

// Bridge connects an engine to a TagDatabase. Add it to the engine as a sink
// (engine.WithSink) so status tags follow the alarms.
type Bridge struct {
	db        *honeycomb.TagDatabase
	eng       *engine.Engine
	prefix    string
	user      string
	pollEvery time.Duration
	feedWait  time.Duration
	onError   func(error)

	statusMu sync.Mutex // serializes status tag writes
	failMu   sync.Mutex
	failing  map[string]bool // sources whose last read failed
}

// New returns a bridge. Call Setup before Run.
func New(db *honeycomb.TagDatabase, eng *engine.Engine, opts ...Option) *Bridge {
	b := &Bridge{
		db: db, eng: eng, prefix: "ALM_", user: "hmi", pollEvery: time.Second, feedWait: 25 * time.Second,
		onError: func(error) {}, failing: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// SetEngine sets the engine when it has to be created after the bridge,
// because the bridge is one of its sinks. Call it before Setup.
func (b *Bridge) SetEngine(eng *engine.Engine) { b.eng = eng }

// StatusTag returns the name of an alarm's status tag.
func (b *Bridge) StatusTag(id string) string { return b.prefix + sanitize(id) }

// CommandTag returns the name of an alarm's command tag.
func (b *Bridge) CommandTag(id string) string { return b.StatusTag(id) + "_CMD" }

func sanitize(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			return r
		}
		return '_'
	}, id)
}

// Setup registers the UDTs, checks that every source tag exists, and creates
// the status and command tags that do not exist yet.
func (b *Bridge) Setup() error {
	honeycomb.RegisterUDT(&AlarmStatus{})
	honeycomb.RegisterUDT(&AlarmCommand{})
	for _, source := range b.eng.Sources() {
		if _, ok := b.db.GetTag(source); !ok {
			return fmt.Errorf("tagbridge: source tag %q does not exist", source)
		}
	}
	for _, s := range b.eng.Statuses() {
		if err := b.ensure(b.StatusTag(s.ID), "Alarm status: "+s.ID, toUDT(s)); err != nil {
			return err
		}
		if err := b.ensure(b.CommandTag(s.ID), "Alarm commands: "+s.ID, &AlarmCommand{}); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bridge) ensure(name, description string, value honeycomb.UDT) error {
	if _, ok := b.db.GetTag(name); ok {
		return nil
	}
	err := b.db.AddTag(&honeycomb.Tag{
		Name:        name,
		Description: description,
		TypeInfo:    &honeycomb.TypeInfo{DataType: value.TypeName()},
		Value:       value,
	})
	if err != nil {
		return fmt.Errorf("tagbridge: add %s: %w", name, err)
	}
	return nil
}

// Run feeds source updates to the engine and runs commands until ctx is done.
func (b *Bridge) Run(ctx context.Context) error {
	type sub struct {
		tag string
		id  uint64
	}
	var subs []sub
	var wg sync.WaitGroup
	defer func() {
		for _, s := range subs {
			_ = b.db.UnsubscribeFromTag(s.tag, s.id) // closes the channel, ending its goroutine
		}
		wg.Wait()
	}()
	// A notification is used only as a signal and the tag is read afresh:
	// receiving the Tag would copy its lock, and the channel delivers the
	// latest state anyway.
	subscribe := func(tag string, handle func()) error {
		ch, id, err := b.db.SubscribeToTag(tag)
		if err != nil {
			return fmt.Errorf("tagbridge: %w", err)
		}
		subs = append(subs, sub{tag, id})
		wg.Go(func() {
			for range ch {
				handle()
			}
		})
		return nil
	}

	// Commands first, so none is missed once alarms start to change; a
	// command set before the subscription is run now.
	for _, s := range b.eng.Statuses() {
		if err := subscribe(b.CommandTag(s.ID), func() { b.command(s.ID) }); err != nil {
			return err
		}
		b.command(s.ID)
		if err := b.writeStatus(s.ID); err != nil {
			b.onError(err)
		}
	}
	var polled []string
	for _, source := range b.eng.Sources() {
		if t, ok := b.db.GetTag(source); ok && t.RemoteAlias != nil {
			polled = append(polled, source) // a remote alias cannot be subscribed to
			continue
		}
		if err := subscribe(source, func() { b.process(source) }); err != nil {
			return err
		}
		b.process(source) // the value before the subscription
	}
	for _, g := range b.groupByDatabase(polled) {
		if feed, ok := g.db.(honeycomb.ChangeSource); ok {
			wg.Go(func() { b.follow(ctx, feed, g) })
		} else {
			wg.Go(func() { b.poll(ctx, g.sources) })
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

// linkGroup is the linked sources of one remote database.
type linkGroup struct {
	id       string
	db       honeycomb.DatabaseAccessor
	sources  []string            // local source tags
	byRemote map[string][]string // remote tag name -> local source tags
	noBatch  bool                // the database has no batch read; set and read by one goroutine
}

// resync reads the current values of a group's sources: with one batch read
// when the database supports it, otherwise one ReadTag per source.
func (b *Bridge) resync(ctx context.Context, g *linkGroup, names []string) {
	reader, ok := g.db.(honeycomb.TagReader)
	if ok && !g.noBatch {
		readings, err := reader.ReadTags(ctx, names)
		if !errors.Is(err, honeycomb.ErrReadTagsUnsupported) {
			for remote, sources := range g.byRemote {
				r, found := readings[remote]
				var readErr error
				if !found || (r.Value == nil && r.Quality == honeycomb.QualityBad) {
					r, readErr = honeycomb.Reading{Quality: honeycomb.QualityBad}, err
					if readErr == nil {
						readErr = fmt.Errorf("no value returned for %s", remote)
					}
				}
				for _, source := range sources {
					b.processReading(source, r, readErr)
				}
			}
			return
		}
		g.noBatch = true // an older server: stop asking
	}
	for _, source := range g.sources {
		b.process(source)
	}
}

func (b *Bridge) groupByDatabase(sources []string) []*linkGroup {
	groups := map[string]*linkGroup{}
	var order []*linkGroup
	for _, source := range sources {
		t, _ := b.db.GetTag(source)
		alias := t.RemoteAlias
		g := groups[alias.DBID]
		if g == nil {
			db, _ := b.db.RegisteredDatabase(alias.DBID)
			g = &linkGroup{id: alias.DBID, db: db, byRemote: map[string][]string{}}
			groups[alias.DBID] = g
			order = append(order, g)
		}
		g.sources = append(g.sources, source)
		g.byRemote[alias.TagName] = append(g.byRemote[alias.TagName], source)
	}
	return order
}

// follow reads a linked database's change feed until ctx is done: every
// change is processed, in order, with its device timestamp, so even a pulse
// shorter than any poll interval raises its alarm. Current values are read
// when following starts and after a gap (changes lost), a restart of the
// remote database or a failed request. A server without a change feed is
// polled instead.
func (b *Bridge) follow(ctx context.Context, feed honeycomb.ChangeSource, g *linkGroup) {
	names := make([]string, 0, len(g.byRemote))
	for name := range g.byRemote {
		names = append(names, name)
	}
	var pos honeycomb.ChangeBatch // empty epoch: start a new reader
	resync := true
	for ctx.Err() == nil {
		batch, err := feed.Changes(ctx, honeycomb.ChangesRequest{
			Since: pos.Next, Epoch: pos.Epoch, Names: names, Wait: b.feedWait, Max: 1000,
		})
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, honeycomb.ErrChangesUnsupported):
			b.onError(fmt.Errorf("tagbridge: database %s has no change feed; polling every %v", g.id, b.pollEvery))
			b.poll(ctx, g.sources)
			return
		case err != nil:
			for _, source := range g.sources {
				b.processReading(source, honeycomb.Reading{Quality: honeycomb.QualityBad}, err)
			}
			resync = true // values may have changed unseen while the link was down
			select {
			case <-ctx.Done():
			case <-time.After(b.pollEvery):
			}
			continue
		}
		if batch.Gap && pos.Epoch != "" {
			b.onError(fmt.Errorf("tagbridge: database %s: changes were lost (buffer overrun or restart); reading current values", g.id))
		}
		if resync || batch.Gap {
			b.resync(ctx, g, names)
			resync = false
		}
		for _, c := range batch.Changes {
			for _, source := range g.byRemote[c.Name] {
				b.processReading(source, honeycomb.Reading{Value: c.Value, Quality: c.Quality, Timestamp: c.Timestamp}, nil)
			}
		}
		pos = batch
	}
}

// poll reads the polled sources now and then every poll interval until ctx is done.
func (b *Bridge) poll(ctx context.Context, sources []string) {
	ticker := time.NewTicker(b.pollEvery)
	defer ticker.Stop()
	for {
		for _, source := range sources {
			if ctx.Err() != nil {
				return
			}
			b.process(source)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// process reads a source and passes it to the engine. A source that cannot be
// read, e.g. a remote database that is unreachable, is passed on as bad
// quality, so the alarms on it hold and bad-quality alarms raise. The error is
// reported once, when the source starts failing.
func (b *Bridge) process(source string) {
	r, err := b.db.ReadTag(source)
	b.processReading(source, r, err)
}

// processReading passes a reading, or the error that prevented it, to the engine.
func (b *Bridge) processReading(source string, r honeycomb.Reading, readErr error) {
	s := evaluator.Sample{Time: time.Now()}
	err := readErr
	if err == nil {
		s, err = ToSample(r)
	}
	b.reportOnce(source, err)
	b.eng.Process(source, s)
}

func (b *Bridge) reportOnce(source string, err error) {
	b.failMu.Lock()
	defer b.failMu.Unlock()
	if err == nil {
		delete(b.failing, source)
		return
	}
	if b.failing[source] {
		return
	}
	b.failing[source] = true
	b.onError(fmt.Errorf("tagbridge: source %s: %w", source, err))
}

// ToSample converts a reading to a sample. BOOL is 0 or 1; integers and reals
// are converted to float64. Good and Uncertain quality count as good.
func ToSample(r honeycomb.Reading) (evaluator.Sample, error) {
	value := r.Value
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	s := evaluator.Sample{
		Good: r.Quality == honeycomb.QualityGood || r.Quality == honeycomb.QualityUncertain,
		Time: r.Timestamp,
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			s.Value = 1
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s.Value = float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s.Value = float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		s.Value = v.Float()
	default:
		s.Good = false
		return s, fmt.Errorf("value of type %T is not a number or BOOL", value)
	}
	return s, nil
}

// Publish implements engine.Sink: it rewrites the status tag of every alarm in events.
func (b *Bridge) Publish(_ context.Context, events []alarm.Event) error {
	var errs []error
	done := make(map[string]bool, len(events))
	for _, e := range events {
		if done[e.Alarm] {
			continue
		}
		done[e.Alarm] = true
		errs = append(errs, b.writeStatus(e.Alarm))
	}
	return errors.Join(errs...)
}

// writeStatus writes an alarm's current status to its status tag. The status
// is read and written under statusMu, so the last write always carries the
// newest status even when writers race.
func (b *Bridge) writeStatus(id string) error {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	s, ok := b.eng.Status(id)
	if !ok {
		return nil
	}
	if err := b.db.SetTagValue(b.StatusTag(id), toUDT(s)); err != nil {
		return fmt.Errorf("tagbridge: status %s: %w", id, err)
	}
	return nil
}

func toUDT(s alarm.Status) *AlarmStatus {
	return &AlarmStatus{
		State:        plc.DINT(s.State),
		InAlarm:      plc.BOOL(s.InAlarm()),
		Acked:        plc.BOOL(s.Acked()),
		InAlarmUnack: plc.BOOL(s.InAlarmUnack()),
		Unacked:      plc.BOOL(s.Unacked()),
		Condition:    plc.BOOL(s.Condition),
		Shelved:      plc.BOOL(s.Shelved),
		Suppressed:   plc.BOOL(s.Suppressed),
		Disabled:     plc.BOOL(s.Disabled),
		Chattering:   plc.BOOL(s.Chattering),
		Priority:     plc.DINT(s.Priority),
		Severity:     plc.DINT(s.Severity),
		AlarmCount:   plc.DINT(s.Count),
		InAlarmTime:  plc.DT(s.InAlarmTime),
		AckTime:      plc.DT(s.AckTime),
		RTNTime:      plc.DT(s.RTNTime),
		ShelveExpiry: plc.DT(s.ShelveExpiry),
	}
}

// command runs the command fields set on an alarm's command tag. The fields
// are read and cleared one at a time through honeycomb, so a field an HMI sets
// while a command runs is not lost.
func (b *Bridge) command(id string) {
	tag := b.CommandTag(id)
	set := func(field string) bool {
		v, err := b.db.GetTagValue(tag + "." + field)
		if err != nil {
			return false
		}
		on, _ := v.(plc.BOOL)
		if on {
			if err := b.db.SetTagValue(tag+"."+field, plc.BOOL(false)); err != nil {
				b.onError(fmt.Errorf("tagbridge: clear %s.%s: %w", tag, field, err))
			}
		}
		return bool(on)
	}
	user := b.user
	if v, err := b.db.GetTagValue(tag + ".User"); err == nil {
		if u, _ := v.(plc.STRING); u != "" {
			user = string(u)
		}
	}

	var errs []error
	ran := false
	run := func(field string, fn func() error) {
		if set(field) {
			ran = true
			errs = append(errs, fn())
		}
	}
	run("Disable", func() error { return b.eng.Disable(id, user) })
	run("Enable", func() error { return b.eng.Enable(id, user) })
	run("Suppress", func() error { return b.eng.Suppress(id, user) })
	run("Unsuppress", func() error { return b.eng.Unsuppress(id, user) })
	run("Shelve", func() error {
		var minutes plc.DINT
		if v, err := b.db.GetTagValue(tag + ".ShelveMinutes"); err == nil {
			minutes, _ = v.(plc.DINT)
		}
		var oneShot plc.BOOL
		if v, err := b.db.GetTagValue(tag + ".OneShot"); err == nil {
			oneShot, _ = v.(plc.BOOL)
		}
		return b.eng.Shelve(id, user, time.Duration(minutes)*time.Minute, bool(oneShot))
	})
	run("Unshelve", func() error { return b.eng.Unshelve(id, user) })
	run("Ack", func() error { return b.eng.Ack(id, user) })
	run("Reset", func() error { return b.eng.Reset(id, user) })
	run("ResetCount", func() error { return b.eng.ResetCount(id, user) })
	if !ran {
		return
	}

	result := "OK"
	if err := errors.Join(errs...); err != nil {
		result = strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	if err := b.db.SetTagValue(tag+".Result", plc.STRING(result)); err != nil {
		b.onError(fmt.Errorf("tagbridge: %s.Result: %w", tag, err))
	}
}
