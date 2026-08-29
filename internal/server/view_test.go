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
	}, now)

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

func TestSpark(t *testing.T) {
	if got := spark([]bool{true}); got != "" {
		t.Errorf("a single sample is not a trend, got %q", got)
	}

	got := spark([]bool{true, false, true})
	if len([]rune(got)) != 3 {
		t.Fatalf("spark = %q, want 3 runes", got)
	}

	// Most recent last, and up and down must be distinguishable.
	runes := []rune(got)
	if runes[0] != runes[2] || runes[0] == runes[1] {
		t.Errorf("spark = %q, want up/down to differ", got)
	}
}

// Only the tail is drawn. Two hundred blocks in a small box is a smear.
func TestSparkIsTruncated(t *testing.T) {
	history := make([]bool, 200)
	for i := range history {
		history[i] = true
	}

	if got := len([]rune(spark(history))); got > 12 {
		t.Errorf("spark width = %d, want at most 12", got)
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
