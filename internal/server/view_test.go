package server

import (
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/probe"
)

func TestGroupsPreserveFirstAppearanceOrder(t *testing.T) {
	now := time.Now()
	got := groupsFrom([]probe.Status{
		{Resource: catalog.Resource{Name: "A", Group: "media"}},
		{Resource: catalog.Resource{Name: "B", Group: ""}},
		{Resource: catalog.Resource{Name: "C", Group: "media"}},
	}, now, 30*time.Second, 30*time.Minute)

	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2", len(got))
	}

	if got[0].Name != "media" || len(got[0].Tiles) != 2 {
		t.Errorf("group 0 = %+v", got[0])
	}

	if got[1].Name != "" || len(got[1].Tiles) != 1 {
		t.Errorf("group 1 = %+v", got[1])
	}
}

func TestTitleForEachState(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	since := now.Add(-90 * time.Minute)

	cases := []struct {
		state probe.State
		err   string
		want  string
	}{
		{probe.StateUp, "", "up for 1h"},
		{probe.StateDown, "connection refused", "down for 1h — connection refused"},
		{probe.StateRetired, "", "retired"},
		{probe.StateUnmonitored, "", "not monitored"},
		{probe.StateUnknown, "", "not probed yet"},
	}

	for _, tc := range cases {
		got := titleFor(probe.Status{State: tc.state, Since: since, Err: tc.err}, now)
		if !strings.Contains(got, tc.want) {
			t.Errorf("state %q: title = %q, want it to contain %q", tc.state, got, tc.want)
		}
	}
}

func states(segs []Segment) string {
	var b strings.Builder

	for _, sg := range segs {
		switch sg.State {
		case "up":
			b.WriteByte('u')
		case "down":
			b.WriteByte('d')
		default:
			b.WriteByte('.')
		}
	}

	return b.String()
}

// An empty history is not a healthy history. A bar that comes up fully green
// after a restart is lying about the past hour.
func TestBarWithNoHistoryIsAllUnknown(t *testing.T) {
	got := states(barFrom(nil, 30*time.Second, 30*time.Minute))

	if strings.Trim(got, ".") != "" {
		t.Errorf("bar = %q, want every segment unknown", got)
	}
}

// Now is the right edge, so a short history fills from the right and leaves
// the older segments blank.
func TestBarFillsFromTheRight(t *testing.T) {
	history := make([]bool, 8)
	for i := range history {
		history[i] = true
	}

	got := states(barFrom(history, 30*time.Second, 30*time.Minute))

	if !strings.HasSuffix(got, "u") {
		t.Errorf("bar = %q, want the newest sample at the right edge", got)
	}

	if !strings.HasPrefix(got, ".") {
		t.Errorf("bar = %q, want blank segments on the left", got)
	}
}

// A bucket is down if ANY sample in it failed. A status bar exists to show the
// blip; averaging a short outage away would defeat the point of drawing one.
func TestOneFailureMarksItsBucketDown(t *testing.T) {
	// Thirty minutes at thirty seconds is 60 samples across 30 segments: 2 per
	// bucket. A single failure must still color its bucket.
	history := make([]bool, 60)
	for i := range history {
		history[i] = true
	}

	history[0] = false

	got := states(barFrom(history, 30*time.Second, 30*time.Minute))

	if strings.Count(got, "d") != 1 {
		t.Errorf("bar = %q, want exactly one down segment", got)
	}

	if !strings.HasPrefix(got, "d") {
		t.Errorf("bar = %q, want the oldest sample in the leftmost segment", got)
	}
}

func TestBarIsAlwaysFullWidth(t *testing.T) {
	for _, n := range []int{0, 1, 7, 30, 120, 500} {
		history := make([]bool, n)
		if got := len(barFrom(history, 30*time.Second, 30*time.Minute)); got != barSegments {
			t.Errorf("history of %d gave %d segments, want %d", n, got, barSegments)
		}
	}
}

// Retired and unmonitored tiles get no bar at all. An empty grey strip would
// invite the question of why it has no history; showing nothing answers it.
func TestNoBarForUnprobedStates(t *testing.T) {
	for _, st := range []probe.State{probe.StateRetired, probe.StateUnmonitored} {
		if got := bar(probe.Status{State: st}, 30*time.Second, 30*time.Minute); got != nil {
			t.Errorf("state %q got a bar of %d segments, want none", st, len(got))
		}
	}
}

func TestHumanize(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "less than a minute",
		20 * time.Minute: "20m",
		5 * time.Hour:    "5h",
		50 * time.Hour:   "2d",
	}

	for d, want := range cases {
		if got := humanize(d); got != want {
			t.Errorf("humanize(%s) = %q, want %q", d, got, want)
		}
	}
}
