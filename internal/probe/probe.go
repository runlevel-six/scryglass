// Package probe decides whether the things on the tile wall are answering.
//
// It is deliberately not a monitoring system. There is no alerting, no
// escalation and no long history, because the question it answers is "what is
// red when I look at the page" and nothing else. That decision is what keeps
// it to a few hundred lines instead of a second application to maintain.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/runlevel-six/scryglass/internal/catalog"
)

// State is what a tile shows.
type State string

const (
	// StateUnknown is a resource that has a check but has not been probed yet.
	StateUnknown State = "unknown"

	// StateUp means the resource answered.
	StateUp State = "up"

	// StateDown means it did not.
	StateDown State = "down"

	// StateRetired is deliberately not running. Dimmed, never alarming.
	StateRetired State = "retired"

	// StateUnmonitored is a resource with no check — a bookmark. It is a link,
	// not a tile that can go red.
	StateUnmonitored State = "unmonitored"
)

// historyLen is how many samples are kept per resource for the sparkline.
//
// At the default interval this is a couple of hours, which is as far back as
// anybody looking at a dashboard actually cares. Keeping a day of samples
// would be a hundred times the memory to draw the same forty pixels.
const historyLen = 240

// Status is everything the UI needs about one resource.
type Status struct {
	Resource catalog.Resource
	State    State

	// Since is when the current state began, so the UI can say "down for 20
	// minutes" rather than only "down".
	Since time.Time

	// Latency is how long the last successful probe took.
	Latency time.Duration

	// Err is why the last probe failed, for the tile's tooltip. Empty when up.
	Err string

	// History is recent results, oldest first. Only meaningful for probed
	// resources.
	History []bool
}

type entry struct {
	res     catalog.Resource
	state   State
	since   time.Time
	latency time.Duration
	err     string
	history []bool
}

// Prober probes resources on an interval and remembers the last result.
type Prober struct {
	client   *http.Client
	interval time.Duration
	now      func() time.Time
	log      *slog.Logger

	mu      sync.RWMutex
	entries map[string]*entry // keyed by resource URL, which is unique per tile
	order   []string          // preserves discovery order for stable rendering

	subsMu sync.Mutex
	subs   map[chan struct{}]struct{}
}

// Options configure a Prober.
type Options struct {
	Interval time.Duration
	Timeout  time.Duration
	Now      func() time.Time // injectable for tests
	Logger   *slog.Logger
}

// New builds a Prober.
func New(opt Options) *Prober {
	if opt.Interval <= 0 {
		opt.Interval = 30 * time.Second
	}

	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Second
	}

	if opt.Now == nil {
		opt.Now = time.Now
	}

	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}

	return &Prober{
		interval: opt.Interval,
		now:      opt.Now,
		log:      opt.Logger,
		entries:  make(map[string]*entry),
		subs:     make(map[chan struct{}]struct{}),
		client: &http.Client{
			Timeout: opt.Timeout,
			// Do not follow redirects. A redirect is an answer, and following
			// it would probe whatever it points at instead — which for
			// anything sitting behind an authenticating proxy means reporting
			// the health of the login page.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				// Certificate verification is deliberately off for probes, and
				// only for probes.
				//
				// These connect to Service DNS names inside the cluster, where
				// applications commonly present self-signed certificates
				// issued for something else entirely. Verification would fail
				// on every one of them and report healthy applications as
				// down. The question being asked is "is this process
				// answering", not "is it who it claims to be" — and the
				// connection never leaves the cluster.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: liveness check, see above
				MaxIdleConns:    32,
				IdleConnTimeout: 90 * time.Second,
			},
		},
	}
}

// SetResources replaces the set being probed, preserving history for anything
// that survived the change.
//
// Discovery rebuilds its list on every pass, so a resource that has not
// changed must not lose its sparkline just because the list was rewritten.
func (p *Prober) SetResources(resources []catalog.Resource) {
	p.mu.Lock()
	defer p.mu.Unlock()

	next := make(map[string]*entry, len(resources))
	order := make([]string, 0, len(resources))

	for _, r := range resources {
		// The URL is the key, so two resources sharing one would otherwise
		// both land in order while only the last survives in entries: one
		// tile drawn twice under the second one's name. The first wins, which
		// is the file entry when the file and discovery both name it.
		if _, dup := next[r.URL]; dup {
			p.log.Warn("duplicate resource URL, keeping the first", "url", r.URL, "dropped", r.Name)

			continue
		}

		order = append(order, r.URL)

		if existing, ok := p.entries[r.URL]; ok {
			existing.res = r
			existing.state = reconcile(existing.state, r)
			next[r.URL] = existing

			continue
		}

		next[r.URL] = &entry{res: r, state: initialState(r), since: p.now()}
	}

	changed := len(order) != len(p.order)

	if !changed {
		for i, url := range order {
			if p.order[i] != url {
				changed = true
				break
			}
		}
	}

	p.entries = next
	p.order = order

	if changed {
		// Discovery adding or removing a tile is as much a reason to redraw as
		// one changing color. Without this, a newly deployed application does
		// not appear until something unrelated happens to flip.
		defer p.notify()
	}
}

// initialState is what a resource looks like before it has ever been probed.
func initialState(r catalog.Resource) State {
	switch {
	case r.Retired:
		return StateRetired
	case r.Check == "":
		return StateUnmonitored
	default:
		return StateUnknown
	}
}

// reconcile keeps an existing state unless the resource's own configuration
// now says it should not be probed at all.
func reconcile(current State, r catalog.Resource) State {
	if s := initialState(r); s != StateUnknown {
		return s
	}

	if current == StateRetired || current == StateUnmonitored {
		return StateUnknown
	}

	return current
}

// Run probes on the interval until the context is canceled.
func (p *Prober) Run(ctx context.Context) {
	// Probe immediately rather than after one interval. A page that shows
	// nothing but "unknown" for the first thirty seconds after a deploy looks
	// broken.
	p.RunOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.RunOnce(ctx)
		}
	}
}

// RunOnce probes every monitored resource once, concurrently.
func (p *Prober) RunOnce(ctx context.Context) {
	p.mu.RLock()

	targets := make([]*entry, 0, len(p.entries))

	for _, e := range p.entries {
		if e.res.Check != "" && !e.res.Retired {
			targets = append(targets, e)
		}
	}

	p.mu.RUnlock()

	// Bounded concurrency. Seventeen simultaneous requests is harmless; four
	// hundred, on a cluster that has grown, is a self-inflicted outage.
	const maxInFlight = 8

	sem := make(chan struct{}, maxInFlight)

	var wg sync.WaitGroup

	changed := make(chan bool, len(targets))

	for _, e := range targets {
		wg.Add(1)

		go func(e *entry) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			changed <- p.probeOne(ctx, e)
		}(e)
	}

	wg.Wait()
	close(changed)

	for c := range changed {
		if c {
			p.notify()

			break
		}
	}
}

func (p *Prober) probeOne(ctx context.Context, e *entry) bool {
	p.mu.RLock()
	url := e.res.Check
	p.mu.RUnlock()

	start := p.now()

	state, latency, errText := p.check(ctx, url, start)

	p.mu.Lock()
	defer p.mu.Unlock()

	prev := e.state
	e.latency = latency
	e.err = errText

	if len(e.history) == historyLen {
		e.history = e.history[1:]
	}

	e.history = append(e.history, state == StateUp)

	if prev != state {
		e.state = state
		e.since = start

		// Logged because a tooltip is not a record. Without this, the only
		// place a failure reason ever appears is a hover on a page somebody
		// has to be looking at, which is no help at all when working out why
		// something went red an hour ago.
		p.log.Info("resource state changed",
			"name", e.res.Name, "from", prev, "to", state,
			"check", url, "error", errText)

		return true
	}

	return false
}

func (p *Prober) check(ctx context.Context, url string, start time.Time) (State, time.Duration, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return StateDown, 0, err.Error()
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return StateUnknown, 0, ""
		}

		return StateDown, 0, err.Error()
	}
	defer resp.Body.Close()

	latency := p.now().Sub(start)

	// Any answer below 500 counts as up, and that is not laziness.
	//
	// Plenty of applications answer / with 401, 403 or 404 and are working
	// perfectly. What is being asked is whether the process is listening and
	// serving, and a 404 proves that as conclusively as a 200. A 5xx is the
	// one class that means the application itself is unwell.
	if resp.StatusCode >= http.StatusInternalServerError {
		return StateDown, latency, resp.Status
	}

	return StateUp, latency, ""
}

// Snapshot returns the current status of every resource, in discovery order.
func (p *Prober) Snapshot() []Status {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]Status, 0, len(p.order))

	for _, url := range p.order {
		e, ok := p.entries[url]
		if !ok {
			continue
		}

		history := make([]bool, len(e.history))
		copy(history, e.history)

		out = append(out, Status{
			Resource: e.res,
			State:    e.state,
			Since:    e.since,
			Latency:  e.latency,
			Err:      e.err,
			History:  history,
		})
	}

	return out
}

// Subscribe returns a channel that receives a value whenever a tile changes
// state. The channel is buffered and sends are dropped if it is full: a slow
// reader must not stall the prober, and a coalesced notification says exactly
// as much as two separate ones.
func (p *Prober) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	p.subsMu.Lock()
	p.subs[ch] = struct{}{}
	p.subsMu.Unlock()

	return ch, func() {
		p.subsMu.Lock()
		delete(p.subs, ch)
		close(ch)
		p.subsMu.Unlock()
	}
}

func (p *Prober) notify() {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()

	for ch := range p.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
