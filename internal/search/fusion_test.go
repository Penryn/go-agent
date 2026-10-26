package search

import (
	"reflect"
	"testing"
)

func TestFusionDeduplicatesEachTrackAndKeepsStableTies(t *testing.T) {
	got := Fuse(3, []string{"a", "a", "b"}, []string{"b", "c"})
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v != %v", got, want)
	}
}
