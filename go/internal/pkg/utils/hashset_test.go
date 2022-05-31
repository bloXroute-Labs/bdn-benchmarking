package utils

import "testing"

func TestHashSet(t *testing.T) {
	var (
		hs   = NewHashSet()
		data = []string{"elem1", "elem2", "elem3"}
	)

	if !hs.Empty() {
		t.Fatal("hash set should be empty")
	}

	for _, v := range data {
		hs.Add(v)
	}

	if hs.Empty() {
		t.Fatalf("hash set should contain %d elements: %#v", len(data), data)
	}

	for _, v := range data {
		if !hs.Contains(v) {
			t.Fatalf("hash set should contain element %q", v)
		}

		hs.Remove(v)
	}

	if !hs.Empty() {
		t.Fatal("hash set should be empty")
	}
}
