package devserver

import (
	"net/http"
	"sort"
	"strconv"
	"time"
)

// The mock clock is real time plus a persisted offset, the mock's stand-in
// for Stripe's test clocks. Advancing it runs every subscription cycle that
// falls due in between, in order, so a month of renewals can happen in one
// call. Every object the mock creates takes its timestamps from now();
// webhook signatures stay on real time so stripe-go's tolerance check passes.
//
// ponytail: one global clock, not one per customer as Stripe has. Map
// /v1/test_helpers/test_clocks onto it if an app's tests need that API.

// now is the mock's current time, at second granularity like every Stripe
// timestamp, so a period end computed from it is reached exactly by an
// advance to that second.
func (m *StripeMock) now() time.Time {
	return time.Now().Add(time.Duration(m.clockOffset.Load()) * time.Second).Truncate(time.Second)
}

// maxClockAdvanceYears bounds one advance: a daily plan run a century ahead
// would hold the lock through tens of thousands of renewals.
const maxClockAdvanceYears = 5

// advanceClock moves the mock clock to `to`, runs every due subscription
// cycle up to it and fires their events. The clock never runs backwards; a
// `to` equal to now only runs what is already due. A zero `to` means "now as
// read under the lock", which is how "Next period" works once real time has
// passed a period end: a now() read before the call could fall a second
// behind by the time the lock is taken. Returns the ids of the
// subscriptions that cycled.
func (m *StripeMock) advanceClock(to time.Time) ([]string, error) {
	to = to.Truncate(time.Second)
	// Checked under the lock so two concurrent advances cannot both pass and
	// then store the smaller offset last.
	m.mu.Lock()
	now := m.now()
	if to.IsZero() {
		to = now
	}
	if to.Before(now) {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusBadRequest, "the mock clock only moves forward (now is %s)", now.Format(time.RFC3339))
	} else if to.After(now.AddDate(maxClockAdvanceYears, 0, 0)) {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusBadRequest, "one advance covers at most %d years; advance again to go further", maxClockAdvanceYears)
	}
	// Offset from the truncated real time so now() lands on `to` exactly.
	m.clockOffset.Store(int64(to.Sub(time.Now().Truncate(time.Second)) / time.Second))
	var fires []webhookFire
	var cycled []string
	// Cycle the earliest due subscription first, repeatedly, so several
	// periods of one subscription and interleaved periods of several run in
	// time order. Each cycle's objects are stamped at their period end, not
	// at `to`.
	for {
		due := m.dueSubscriptionsLocked(to)
		if len(due) == 0 {
			break
		}
		sub := due[0]
		fires = append(fires, m.cycleSubscriptionLocked(sub, sub.dueAt())...)
		cycled = append(cycled, sub.ID)
	}
	m.persist()
	m.mu.Unlock()

	if len(fires) > 0 {
		m.fireEventsAsync(fires, "clock", to.Format(time.RFC3339))
	}
	return cycled, nil
}

// dueSubscriptionsLocked lists live subscriptions whose period end or
// scheduled cancel_at has arrived by `at`, earliest first. Caller holds m.mu.
func (m *StripeMock) dueSubscriptionsLocked(at time.Time) []*stripeSubscription {
	var due []*stripeSubscription
	for _, s := range m.subscriptions {
		if s.Status != "canceled" && !s.dueAt().After(at) {
			due = append(due, s)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].dueAt().Equal(due[j].dueAt()) {
			return due[i].dueAt().Before(due[j].dueAt())
		}
		return due[i].ID < due[j].ID
	})
	return due
}

// parseClockTarget turns the dashboard/MCP inputs into a target time: `by`
// is a duration like 1d, 2w, 1m, 1y (or anything time.ParseDuration reads),
// `to` is RFC 3339 or unix seconds. Exactly one is set.
func (m *StripeMock) parseClockTarget(by, to string) (time.Time, error) {
	switch {
	case by != "" && to != "":
		return time.Time{}, stripeErr(http.StatusBadRequest, "pass either by or to, not both")
	case to != "":
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			return t, nil
		}
		if unix, err := strconv.ParseInt(to, 10, 64); err == nil && unix > 0 {
			return time.Unix(unix, 0), nil
		}
		return time.Time{}, stripeErr(http.StatusBadRequest, "to must be RFC 3339 or unix seconds, got %q", to)
	case by != "":
		now := m.now()
		// Whole-string match, so "1m30s" and "5ms" fall through to
		// time.ParseDuration instead of being read as months.
		if n, err := strconv.Atoi(by[:len(by)-1]); err == nil && n > 0 {
			switch by[len(by)-1] {
			case 'd':
				return now.AddDate(0, 0, n), nil
			case 'w':
				return now.AddDate(0, 0, 7*n), nil
			case 'm':
				return addMonthsClamped(now, n), nil // same month math as subscription periods
			case 'y':
				return addMonthsClamped(now, 12*n), nil
			}
		}
		d, err := time.ParseDuration(by)
		if err != nil || d <= 0 {
			return time.Time{}, stripeErr(http.StatusBadRequest, "by must be like 1d, 2w, 1m, 1y or a Go duration, got %q", by)
		}
		return now.Add(d), nil
	}
	return time.Time{}, stripeErr(http.StatusBadRequest, "by or to is required")
}
