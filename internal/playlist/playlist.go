// Package playlist holds the queue of tracks and decides what plays next.
//
// It knows nothing about ffmpeg, Discord or the UI: it is pure ordering logic,
// so the awkward parts — what "next" means under repeat-one, what happens to
// the order when shuffle is toggled mid-track — can be tested without an audio
// device or a network connection.
package playlist

import (
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Repeat is what happens when playback reaches the end of the order.
type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatAll Repeat = "all"
	RepeatOne Repeat = "one"
)

// Valid reports whether r is a mode this package understands. A hand-edited
// configuration file is the only way to get anything else.
func (r Repeat) Valid() bool {
	switch r {
	case RepeatOff, RepeatAll, RepeatOne:
		return true
	}
	return false
}

// Source is where a track's audio comes from.
type Source string

const (
	SourceFile    Source = "file"
	SourceYouTube Source = "youtube"
)

// Valid reports whether s is a source this build can play. A queue written by a
// newer build, carrying a source this one has never heard of, is the way
// anything else gets here.
func (s Source) Valid() bool {
	switch s {
	case SourceFile, SourceYouTube:
		return true
	}
	return false
}

// Track is one entry in the queue.
//
// The same file may be queued more than once, so ID rather than Path
// identifies an entry. Everything below Source is metadata read after the track
// was added, and may still be empty while that read is in flight.
type Track struct {
	ID string `json:"id"`

	// Path is a filesystem path, or the page URL when Source is SourceYouTube.
	// One field rather than two because every consumer — persistence, the
	// empty-means-invalid check below, the row tooltip — wants "the thing this
	// entry points at" and none of them care which kind it is.
	Path   string `json:"path"`
	Source Source `json:"source"`

	Name       string `json:"name"`
	Codec      string `json:"codec"`
	DurationMs int64  `json:"durationMs"`
}

// State is the whole queue: what the UI renders and what is written to disk.
type State struct {
	Tracks    []Track `json:"tracks"`
	CurrentID string  `json:"currentId"`
	Shuffle   bool    `json:"shuffle"`
	Repeat    Repeat  `json:"repeat"`
}

// nextID numbers new entries. It is process-wide rather than per-list because
// ids only have to be unique within one queue, and a single counter cannot
// produce a collision when two lists are alive at once.
var nextID atomic.Uint64

// NewID returns an identifier for a new track.
func NewID() string {
	return strconv.FormatUint(nextID.Add(1), 36)
}

// List is a queue of tracks with a playback order over it.
//
// Two orderings are kept at once: tracks is insertion order, which is what the
// user sees and drags around, and order is the sequence playback follows. They
// are the same slice contents unless shuffle is on. Holding the playback order
// as ids rather than indices is what makes an edit cheap: a move touches only
// tracks, a remove filters both, and neither can leave the two disagreeing
// about which entry is which.
type List struct {
	mu        sync.RWMutex
	tracks    []Track
	order     []string
	currentID string
	shuffle   bool
	repeat    Repeat
	rng       *rand.Rand
}

// New creates a list from saved state. Anything inconsistent in that state —
// an unknown repeat mode, a current id naming no entry — is corrected here
// rather than trusted, since it comes off disk.
func New(state State) *List {
	l := &List{
		repeat: state.Repeat,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	if !l.repeat.Valid() {
		l.repeat = RepeatOff
	}

	for _, t := range state.Tracks {
		if t, ok := sanitise(t); ok {
			l.tracks = append(l.tracks, t)
		}
	}

	l.currentID = state.CurrentID
	if l.indexOf(l.currentID) < 0 {
		l.currentID = ""
	}

	// The shuffled order is not persisted: it is regenerated here, so a
	// restart reshuffles rather than replaying the previous run's sequence.
	l.shuffle = state.Shuffle
	l.rebuildOrder()
	return l
}

// Snapshot returns a copy of the queue, safe to serialise or hand to the UI.
func (l *List) Snapshot() State {
	l.mu.RLock()
	defer l.mu.RUnlock()

	tracks := make([]Track, len(l.tracks))
	copy(tracks, l.tracks)

	return State{
		Tracks:    tracks,
		CurrentID: l.currentID,
		Shuffle:   l.shuffle,
		Repeat:    l.repeat,
	}
}

// Len returns how many tracks are queued.
func (l *List) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.tracks)
}

// Add appends tracks, giving any without one an id.
func (l *List) Add(tracks ...Track) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, t := range tracks {
		t, ok := sanitise(t)
		if !ok {
			continue
		}
		l.tracks = append(l.tracks, t)

		// Appending to the order rather than reshuffling keeps whatever is
		// playing, and everything already scheduled after it, exactly where it
		// was. New tracks join the end either way.
		l.order = append(l.order, t.ID)
	}
}

// SetMetadata fills in what was learned by reading the file, which arrives
// after the track was added. It reports whether the entry still exists: it may
// have been removed while the read was in flight.
func (l *List) SetMetadata(id, name, codec string, durationMs int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	i := l.indexOf(id)
	if i < 0 {
		return false
	}
	if name != "" {
		l.tracks[i].Name = name
	}
	l.tracks[i].Codec = codec
	l.tracks[i].DurationMs = durationMs
	return true
}

// Remove drops an entry.
//
// Removing whatever is current moves current to what would have played next,
// so deleting the playing track and then pressing next does the obvious thing
// rather than starting again from the top.
func (l *List) Remove(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	i := l.indexOf(id)
	if i < 0 {
		return
	}

	if id == l.currentID {
		l.currentID = l.peek(id, 1, RepeatOff)
	}

	l.tracks = append(l.tracks[:i], l.tracks[i+1:]...)
	for j, oid := range l.order {
		if oid == id {
			l.order = append(l.order[:j], l.order[j+1:]...)
			break
		}
	}
}

// Move repositions an entry within the visible list. It has no effect on the
// playback order while shuffle is on, which is the point of shuffle.
func (l *List) Move(id string, to int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	from := l.indexOf(id)
	if from < 0 {
		return
	}
	if to < 0 {
		to = 0
	}
	if to >= len(l.tracks) {
		to = len(l.tracks) - 1
	}
	if from == to {
		return
	}

	t := l.tracks[from]
	l.tracks = append(l.tracks[:from], l.tracks[from+1:]...)
	l.tracks = append(l.tracks[:to], append([]Track{t}, l.tracks[to:]...)...)

	if !l.shuffle {
		l.rebuildOrder()
	}
}

// Clear empties the queue.
func (l *List) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.tracks = nil
	l.order = nil
	l.currentID = ""
}

// Select makes an entry current without starting it.
func (l *List) Select(id string) (Track, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	i := l.indexOf(id)
	if i < 0 {
		return Track{}, false
	}
	l.currentID = id
	return l.tracks[i], true
}

// Current returns the entry playback is on, falling back to the first in the
// order when nothing has been chosen yet. That fallback is what lets Play work
// on a queue nobody has clicked into.
func (l *List) Current() (Track, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if i := l.indexOf(l.currentID); i >= 0 {
		return l.tracks[i], true
	}
	if len(l.order) == 0 {
		return Track{}, false
	}

	l.currentID = l.order[0]
	return l.tracks[l.indexOf(l.currentID)], true
}

// Next advances to the following track and returns it.
//
// auto distinguishes a track that ended by itself from the user pressing next.
// Only the former repeats under RepeatOne: someone who presses next has asked
// to hear something else, and replaying the same track would look broken.
func (l *List) Next(auto bool) (Track, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.order) == 0 {
		return Track{}, false
	}
	if l.currentID == "" {
		l.currentID = l.order[0]
		return l.tracks[l.indexOf(l.currentID)], true
	}

	if auto && l.repeat == RepeatOne {
		return l.tracks[l.indexOf(l.currentID)], true
	}

	// A manual next under repeat-one still walks the list, and wrapping at the
	// end is the friendlier reading of "repeat this one" than stopping dead.
	wrap := l.repeat
	if wrap == RepeatOne {
		wrap = RepeatAll
	}

	id := l.peek(l.currentID, 1, wrap)
	if id == "" {
		return Track{}, false
	}
	l.currentID = id
	return l.tracks[l.indexOf(id)], true
}

// Prev steps back one track. It wraps unless repeat is off, matching Next.
func (l *List) Prev() (Track, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.order) == 0 {
		return Track{}, false
	}
	if l.currentID == "" {
		l.currentID = l.order[0]
		return l.tracks[l.indexOf(l.currentID)], true
	}

	wrap := l.repeat
	if wrap == RepeatOne {
		wrap = RepeatAll
	}

	id := l.peek(l.currentID, -1, wrap)
	if id == "" {
		return Track{}, false
	}
	l.currentID = id
	return l.tracks[l.indexOf(id)], true
}

// SetShuffle turns shuffling on or off, rebuilding the playback order.
func (l *List) SetShuffle(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.shuffle == on {
		return
	}
	l.shuffle = on
	l.rebuildOrder()
}

// Shuffle reports whether the order is shuffled.
func (l *List) Shuffle() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.shuffle
}

// SetRepeat sets the repeat mode, ignoring anything unrecognised.
func (l *List) SetRepeat(r Repeat) {
	if !r.Valid() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.repeat = r
}

// Repeat returns the repeat mode.
func (l *List) Repeat() Repeat {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.repeat
}

// ------------------------------------------------------------------ internal

// sanitise vets one entry on its way into the list and reports whether it is
// worth keeping. Both entry points share it: tracks arriving from disk have had
// no validation at all, and tracks built in code are only a little more
// trustworthy.
//
// An unrecognised source is dropped rather than corrected. Guessing that it
// means SourceFile would hand a URL written by some later build to ffmpeg as
// though it were a path, and losing the row is the better failure — it matches
// what an entry pointing at nothing has always done.
func sanitise(t Track) (Track, bool) {
	if t.Path == "" {
		return Track{}, false
	}
	if t.Source == "" {
		// Queues written before tracks had a source hold local files only.
		t.Source = SourceFile
	}
	if !t.Source.Valid() {
		return Track{}, false
	}
	if t.ID == "" {
		t.ID = NewID()
	}
	return t, true
}

// Everything below assumes l.mu is already held.

func (l *List) indexOf(id string) int {
	if id == "" {
		return -1
	}
	for i, t := range l.tracks {
		if t.ID == id {
			return i
		}
	}
	return -1
}

func (l *List) orderIndexOf(id string) int {
	for i, oid := range l.order {
		if oid == id {
			return i
		}
	}
	return -1
}

// peek returns the id step places from id in the playback order, or "" if that
// falls off the end and repeat does not wrap.
func (l *List) peek(id string, step int, repeat Repeat) string {
	i := l.orderIndexOf(id)
	if i < 0 || len(l.order) == 0 {
		return ""
	}

	next := i + step
	if next < 0 || next >= len(l.order) {
		if repeat != RepeatAll {
			return ""
		}
		next = (next%len(l.order) + len(l.order)) % len(l.order)
	}
	return l.order[next]
}

// rebuildOrder regenerates the playback order from the current tracks.
//
// A shuffled order always starts with whatever is playing, so toggling shuffle
// mid-track reorders what comes after it instead of cutting it off.
func (l *List) rebuildOrder() {
	l.order = make([]string, 0, len(l.tracks))

	if !l.shuffle {
		for _, t := range l.tracks {
			l.order = append(l.order, t.ID)
		}
		return
	}

	rest := make([]string, 0, len(l.tracks))
	for _, t := range l.tracks {
		if t.ID == l.currentID {
			continue
		}
		rest = append(rest, t.ID)
	}
	l.rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })

	if l.indexOf(l.currentID) >= 0 {
		l.order = append(l.order, l.currentID)
	}
	l.order = append(l.order, rest...)
}
