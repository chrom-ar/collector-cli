package follow

import (
	"strconv"
	"testing"
)

func TestSeenForgetsTheOldestPastItsSize(t *testing.T) {
	s := newSeen(3)
	for _, id := range []string{"a", "b", "c"} {
		if !s.add(id) {
			t.Fatalf("%s seen before", id)
		}
	}
	if s.add("b") {
		t.Error("b added twice")
	}
	s.add("d") // forgets a
	if !s.add("a") {
		t.Error("a still remembered past the size")
	}
	if s.add("d") {
		t.Error("d forgotten too early")
	}
}

func TestSeenDefaultSize(t *testing.T) {
	s := newSeen(seenMax)
	for i := range seenMax + 1 {
		s.add(strconv.Itoa(i))
	}
	if !s.add("0") {
		t.Error("the oldest of 10,001 still remembered")
	}
	if s.add(strconv.Itoa(seenMax)) {
		t.Error("the newest forgotten")
	}
	if seenMax != 10_000 {
		t.Errorf("seenMax = %d", seenMax)
	}
}
