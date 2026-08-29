package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/runlevel-six/scryglass/internal/probe"
)

// tile is one square on the wall, already formatted for display. Templates do
// no arithmetic; everything they show is decided here where it can be tested.
type tile struct {
	Name  string
	URL   string
	State probe.State
	Title string
	Spark string
}

type group struct {
	Name  string
	Tiles []tile
}

// groupsFrom turns prober statuses into what the template renders.
//
// Grouping preserves first-appearance order rather than sorting alphabetically:
// the catalog file is written in the order somebody wanted to see things, and
// re-sorting it discards that.
func groupsFrom(statuses []probe.Status, now time.Time) []group {
	var (
		order  []string
		byName = map[string][]tile{}
	)

	for _, s := range statuses {
		name := s.Resource.Group

		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}

		byName[name] = append(byName[name], tileFrom(s, now))
	}

	out := make([]group, 0, len(order))
	for _, name := range order {
		out = append(out, group{Name: name, Tiles: byName[name]})
	}

	return out
}

func tileFrom(s probe.Status, now time.Time) tile {
	return tile{
		Name:  s.Resource.Name,
		URL:   s.Resource.URL,
		State: s.State,
		Title: titleFor(s, now),
		Spark: spark(s.History),
	}
}

// titleFor is the tooltip: what state, for how long, and why if it is bad.
func titleFor(s probe.Status, now time.Time) string {
	switch s.State {
	case probe.StateUnmonitored:
		return "not monitored"
	case probe.StateRetired:
		return "retired"
	case probe.StateUnknown:
		return "not probed yet"
	case probe.StateDown:
		msg := "down for " + humanize(now.Sub(s.Since))
		if s.Err != "" {
			msg += " — " + s.Err
		}

		return msg
	case probe.StateUp:
		return fmt.Sprintf("up for %s — %dms", humanize(now.Sub(s.Since)), s.Latency.Milliseconds())
	default:
		return ""
	}
}

// humanize renders a duration the way somebody glancing at a tile reads one.
// Nobody wants "1h23m11.238s".
func humanize(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// sparkChars are drawn from the block elements so the sparkline needs no SVG,
// no canvas and no second request — it is text, and it inherits the tile's
// color for free.
const sparkChars = "▁█"

// spark renders recent history as a run of blocks, most recent last.
//
// Only the last few samples are shown. A tile is glanceable or it is not, and
// two hundred blocks in a 40-pixel box is a smear rather than information.
func spark(history []bool) string {
	const width = 12

	if len(history) < 2 {
		return ""
	}

	if len(history) > width {
		history = history[len(history)-width:]
	}

	var b strings.Builder

	for _, ok := range history {
		if ok {
			b.WriteString(sparkChars[3:])
		} else {
			b.WriteString(sparkChars[:3])
		}
	}

	return b.String()
}
