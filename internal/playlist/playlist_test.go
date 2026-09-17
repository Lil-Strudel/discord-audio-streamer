package playlist

import (
	"sort"
	"testing"
)

// listOf builds a queue whose track ids and names are the given labels, so a
// test can assert on "a", "b", "c" rather than on generated ids.
func listOf(labels ...string) *List {
	state := State{Repeat: RepeatOff}
	for _, label := range labels {
		state.Tracks = append(state.Tracks, Track{ID: label, Path: "/music/" + label + ".mp3", Name: label})
	}
	return New(state)
}

// ids returns the visible track order.
func ids(l *List) []string {
	var out []string
	for _, t := range l.Snapshot().Tracks {
		out = append(out, t.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// advance asserts what Next yields, as an id, or "" when it refuses to move.
func advance(t *testing.T, l *List, auto bool) string {
	t.Helper()
	track, ok := l.Next(auto)
	if !ok {
		return ""
	}
	return track.ID
}

func TestNewDropsTracksWithoutAPath(t *testing.T) {
	l := New(State{Tracks: []Track{
		{ID: "a", Path: "/music/a.mp3"},
		{ID: "b"},
		{ID: "c", Path: "/music/c.mp3"},
	}})
	if got := ids(l); !equal(got, []string{"a", "c"}) {
		t.Fatalf("got %v, want [a c]", got)
	}
}

func TestNewClearsACurrentIDNamingNoTrack(t *testing.T) {
	l := New(State{
		Tracks:    []Track{{ID: "a", Path: "/music/a.mp3"}},
		CurrentID: "gone",
	})
	if got := l.Snapshot().CurrentID; got != "" {
		t.Fatalf("currentId = %q, want empty", got)
	}
}

func TestNewRejectsAnUnknownRepeatMode(t *testing.T) {
	l := New(State{Repeat: Repeat("sideways")})
	if got := l.Repeat(); got != RepeatOff {
		t.Fatalf("repeat = %q, want %q", got, RepeatOff)
	}
}

func TestCurrentFallsBackToTheFirstTrack(t *testing.T) {
	l := listOf("a", "b", "c")
	track, ok := l.Current()
	if !ok || track.ID != "a" {
		t.Fatalf("got %q ok=%v, want a", track.ID, ok)
	}
	if got := l.Snapshot().CurrentID; got != "a" {
		t.Fatalf("currentId = %q, want a", got)
	}
}

func TestCurrentOnAnEmptyQueue(t *testing.T) {
	if _, ok := listOf().Current(); ok {
		t.Fatal("an empty queue reported a current track")
	}
}

func TestNextWalksTheQueueAndStopsWithRepeatOff(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Select("a")

	for _, want := range []string{"b", "c", ""} {
		if got := advance(t, l, true); got != want {
			t.Fatalf("next = %q, want %q", got, want)
		}
	}
}

func TestNextWrapsWithRepeatAll(t *testing.T) {
	l := listOf("a", "b")
	l.SetRepeat(RepeatAll)
	l.Select("b")

	if got := advance(t, l, true); got != "a" {
		t.Fatalf("next = %q, want a", got)
	}
}

func TestRepeatOneReplaysOnlyWhenTheTrackEndedByItself(t *testing.T) {
	l := listOf("a", "b", "c")
	l.SetRepeat(RepeatOne)
	l.Select("b")

	// A track that ran out repeats.
	if got := advance(t, l, true); got != "b" {
		t.Fatalf("automatic next = %q, want b", got)
	}
	// Pressing next moves on regardless.
	if got := advance(t, l, false); got != "c" {
		t.Fatalf("manual next = %q, want c", got)
	}
	// And wraps rather than stopping, since repeat is on in some form.
	if got := advance(t, l, false); got != "a" {
		t.Fatalf("manual next at the end = %q, want a", got)
	}
}

func TestNextOnAnUntouchedQueueStartsAtTheTop(t *testing.T) {
	l := listOf("a", "b")
	if got := advance(t, l, true); got != "a" {
		t.Fatalf("next = %q, want a", got)
	}
}

func TestNextOnAnEmptyQueue(t *testing.T) {
	if _, ok := listOf().Next(true); ok {
		t.Fatal("an empty queue produced a next track")
	}
}

func TestPrevStepsBackAndStopsWithRepeatOff(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Select("b")

	track, ok := l.Prev()
	if !ok || track.ID != "a" {
		t.Fatalf("prev = %q ok=%v, want a", track.ID, ok)
	}
	if _, ok := l.Prev(); ok {
		t.Fatal("prev walked off the front of the queue")
	}
}

func TestPrevWrapsWithRepeatAll(t *testing.T) {
	l := listOf("a", "b", "c")
	l.SetRepeat(RepeatAll)
	l.Select("a")

	track, ok := l.Prev()
	if !ok || track.ID != "c" {
		t.Fatalf("prev = %q ok=%v, want c", track.ID, ok)
	}
}

func TestAddAppendsToBothOrders(t *testing.T) {
	l := listOf("a")
	l.Add(Track{ID: "b", Path: "/music/b.mp3"}, Track{ID: "c", Path: "/music/c.mp3"})

	if got := ids(l); !equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v, want [a b c]", got)
	}

	l.Select("a")
	for _, want := range []string{"b", "c"} {
		if got := advance(t, l, true); got != want {
			t.Fatalf("next = %q, want %q", got, want)
		}
	}
}

func TestAddGivesNewTracksAnID(t *testing.T) {
	l := listOf()
	l.Add(Track{Path: "/music/a.mp3"}, Track{Path: "/music/b.mp3"})

	tracks := l.Snapshot().Tracks
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].ID == "" || tracks[0].ID == tracks[1].ID {
		t.Fatalf("ids %q and %q are not distinct and non-empty", tracks[0].ID, tracks[1].ID)
	}
}

func TestAddIgnoresTracksWithoutAPath(t *testing.T) {
	l := listOf("a")
	l.Add(Track{ID: "b"})
	if got := ids(l); !equal(got, []string{"a"}) {
		t.Fatalf("got %v, want [a]", got)
	}
}

func TestSetMetadata(t *testing.T) {
	l := listOf("a")
	if !l.SetMetadata("a", "Artist — Song", "mp3", 214_000) {
		t.Fatal("SetMetadata reported the track was gone")
	}

	track := l.Snapshot().Tracks[0]
	if track.Name != "Artist — Song" || track.Codec != "mp3" || track.DurationMs != 214_000 {
		t.Fatalf("got %+v", track)
	}
	if l.SetMetadata("gone", "x", "mp3", 1) {
		t.Fatal("SetMetadata accepted an unknown id")
	}
}

func TestSetMetadataKeepsTheExistingNameWhenTheFileHasNoTags(t *testing.T) {
	l := listOf("a")
	l.SetMetadata("a", "", "flac", 1000)
	if got := l.Snapshot().Tracks[0].Name; got != "a" {
		t.Fatalf("name = %q, want the original a", got)
	}
}

func TestRemoveMovesCurrentToTheFollowingTrack(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Select("b")
	l.Remove("b")

	if got := ids(l); !equal(got, []string{"a", "c"}) {
		t.Fatalf("got %v, want [a c]", got)
	}
	if got := l.Snapshot().CurrentID; got != "c" {
		t.Fatalf("currentId = %q, want c", got)
	}
}

func TestRemovingTheLastCurrentTrackLeavesNothingCurrent(t *testing.T) {
	l := listOf("a", "b")
	l.Select("b")
	l.Remove("b")

	if got := l.Snapshot().CurrentID; got != "" {
		t.Fatalf("currentId = %q, want empty", got)
	}
}

func TestRemoveKeepsTheOrderUsable(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Select("a")
	l.Remove("b")

	if got := advance(t, l, true); got != "c" {
		t.Fatalf("next = %q, want c", got)
	}
}

func TestMoveReordersTheQueueAndThePlaybackOrder(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Move("c", 0)

	if got := ids(l); !equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("got %v, want [c a b]", got)
	}

	l.Select("c")
	if got := advance(t, l, true); got != "a" {
		t.Fatalf("next = %q, want a", got)
	}
}

func TestMoveClampsAnOutOfRangeTarget(t *testing.T) {
	l := listOf("a", "b", "c")
	l.Move("a", 99)
	if got := ids(l); !equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("got %v, want [b c a]", got)
	}

	l.Move("a", -1)
	if got := ids(l); !equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v, want [a b c]", got)
	}
}

func TestMoveLeavesThePlaybackOrderAloneWhileShuffled(t *testing.T) {
	l := listOf("a", "b", "c", "d")
	l.Select("a")
	l.SetShuffle(true)

	before := l.order
	l.Move("d", 0)

	if !equal(l.order, before) {
		t.Fatalf("shuffled order changed from %v to %v", before, l.order)
	}
	if got := ids(l); !equal(got, []string{"d", "a", "b", "c"}) {
		t.Fatalf("visible order = %v, want [d a b c]", got)
	}
}

func TestClear(t *testing.T) {
	l := listOf("a", "b")
	l.Select("a")
	l.Clear()

	if l.Len() != 0 {
		t.Fatalf("got %d tracks, want 0", l.Len())
	}
	if got := l.Snapshot().CurrentID; got != "" {
		t.Fatalf("currentId = %q, want empty", got)
	}
	if _, ok := l.Next(true); ok {
		t.Fatal("a cleared queue produced a next track")
	}
}

func TestShuffleKeepsTheCurrentTrackFirst(t *testing.T) {
	l := listOf("a", "b", "c", "d", "e")
	l.Select("c")
	l.SetShuffle(true)

	if l.order[0] != "c" {
		t.Fatalf("shuffled order starts with %q, want the playing track c", l.order[0])
	}
}

func TestShuffleCoversEveryTrackExactlyOnce(t *testing.T) {
	l := listOf("a", "b", "c", "d", "e")
	l.Select("a")
	l.SetShuffle(true)

	seen := append([]string(nil), l.order...)
	sort.Strings(seen)
	if !equal(seen, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("shuffled order %v covers %v", l.order, seen)
	}
}

func TestUnshufflingRestoresTheVisibleOrder(t *testing.T) {
	l := listOf("a", "b", "c", "d")
	l.Select("c")
	l.SetShuffle(true)
	l.SetShuffle(false)

	if !equal(l.order, []string{"a", "b", "c", "d"}) {
		t.Fatalf("order = %v, want [a b c d]", l.order)
	}
	if got := l.Snapshot().CurrentID; got != "c" {
		t.Fatalf("currentId = %q, want c", got)
	}
}

func TestShuffleVisitsEveryTrackBeforeWrapping(t *testing.T) {
	l := listOf("a", "b", "c", "d", "e")
	l.SetRepeat(RepeatAll)
	l.Select("a")
	l.SetShuffle(true)

	seen := map[string]bool{"a": true}
	for range 4 {
		seen[advance(t, l, true)] = true
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d distinct tracks over a full pass, want 5", len(seen))
	}
}

func TestTracksAddedWhileShuffledAreStillReached(t *testing.T) {
	l := listOf("a", "b")
	l.Select("a")
	l.SetShuffle(true)
	l.Add(Track{ID: "c", Path: "/music/c.mp3"})

	seen := map[string]bool{"a": true}
	for range 2 {
		seen[advance(t, l, true)] = true
	}
	if !seen["c"] {
		t.Fatalf("the track added while shuffled was never played; saw %v", seen)
	}
}

func TestSnapshotDoesNotShareItsBackingArray(t *testing.T) {
	l := listOf("a", "b")
	snap := l.Snapshot()
	snap.Tracks[0].Name = "mutated"

	if got := l.Snapshot().Tracks[0].Name; got != "a" {
		t.Fatalf("name = %q, want a: the snapshot aliased the list", got)
	}
}

func TestSelectRejectsAnUnknownID(t *testing.T) {
	l := listOf("a")
	if _, ok := l.Select("gone"); ok {
		t.Fatal("Select accepted an unknown id")
	}
}

func TestSetRepeatIgnoresAnUnknownMode(t *testing.T) {
	l := listOf("a")
	l.SetRepeat(RepeatAll)
	l.SetRepeat(Repeat("backwards"))
	if got := l.Repeat(); got != RepeatAll {
		t.Fatalf("repeat = %q, want %q", got, RepeatAll)
	}
}
