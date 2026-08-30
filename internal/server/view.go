package server

import (
	"fmt"
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
	Bar   []Segment
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
func groupsFrom(statuses []probe.Status, now time.Time, interval, window time.Duration) []group {
	var (
		order  []string
		byName = map[string][]tile{}
	)

	for _, s := range statuses {
		name := s.Resource.Group

		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}

		byName[name] = append(byName[name], tileFrom(s, now, interval, window))
	}

	// The unnamed group goes last and gets a label, but only when there is
	// something to distinguish it from.
	//
	// Rendered unlabeled and in place, it reads as belonging to whichever
	// group happens to precede it — which is exactly what discovered tiles did
	// underneath a hand-written "personal" heading. When nothing is grouped at
	// all, a lone "Other" heading over the whole wall is worse than none.
	named := 0

	for _, name := range order {
		if name != "" {
			named++
		}
	}

	out := make([]group, 0, len(order))

	for _, name := range order {
		if name == "" {
			continue
		}

		out = append(out, group{Name: name, Tiles: byName[name]})
	}

	if rest, ok := byName[""]; ok {
		label := ""
		if named > 0 {
			label = "other"
		}

		out = append(out, group{Name: label, Tiles: rest})
	}

	return out
}

func tileFrom(s probe.Status, now time.Time, interval, window time.Duration) tile {
	return tile{
		Name:  s.Resource.Name,
		URL:   s.Resource.URL,
		State: s.State,
		Title: titleFor(s, now),
		Bar:   bar(s, interval, window),
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

// barSegments is how many blocks the status bar is drawn with.
//
// Chosen against the tile width rather than the data: at thirty, each block is
// a couple of pixels in a tile narrow enough to fit four across, which is wide
// enough to see and narrow enough that a single blip does not dominate. More
// segments than this is a smear.
const barSegments = 30

// Segment is one block of the status bar.
type Segment struct {
	// State is "up", "down", or "none" for a stretch with no samples yet.
	State string

	// Title is the tooltip for this block, so hovering says when.
	Title string
}

// barFrom buckets recent history into fixed-width segments.
//
// A bucket is down if ANY sample in it failed, deliberately. A status bar
// exists to show the blip; averaging a thirty-second outage away across a
// two-minute bucket would defeat the only reason to draw one.
//
// Segments with no samples render as "none" rather than as up. An empty
// history is not a healthy history, and a bar that starts fully green after a
// restart is lying about the past hour.
func barFrom(history []bool, interval, window time.Duration) []Segment {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	wanted := int(window / interval)
	if wanted < barSegments {
		wanted = barSegments
	}

	tail := history
	if len(tail) > wanted {
		tail = tail[len(tail)-wanted:]
	}

	perSegment := wanted / barSegments
	if perSegment < 1 {
		perSegment = 1
	}

	segs := make([]Segment, barSegments)

	for i := range segs {
		// Segments are filled from the right, because the right edge is now.
		fromRight := barSegments - 1 - i

		end := len(tail) - fromRight*perSegment
		start := end - perSegment

		if start < 0 {
			start = 0
		}

		age := time.Duration(fromRight*perSegment) * interval

		switch {
		case end <= 0 || start >= end:
			segs[i] = Segment{State: "none", Title: "no data"}
		default:
			state := "up"

			for _, ok := range tail[start:end] {
				if !ok {
					state = "down"
					break
				}
			}

			segs[i] = Segment{State: state, Title: state + " " + agoLabel(age)}
		}
	}

	return segs
}

func agoLabel(age time.Duration) string {
	if age < time.Minute {
		return "just now"
	}

	return humanize(age) + " ago"
}

// bar is empty for anything that is not probed. A retired or unmonitored tile
// showing an empty grey strip would invite the question of why it has no
// history; showing nothing answers it.
func bar(s probe.Status, interval, window time.Duration) []Segment {
	if s.State == probe.StateRetired || s.State == probe.StateUnmonitored {
		return nil
	}

	return barFrom(s.History, interval, window)
}
