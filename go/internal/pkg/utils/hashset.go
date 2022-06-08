package utils

// HashSet represents a string hash set.
type HashSet map[string]struct{}

// NewHashSet creates a new HashSet
func NewHashSet() HashSet {
	return make(HashSet)
}

// Contains checks if a set contains specified element.
func (hs HashSet) Contains(elem string) bool {
	_, ok := hs[elem]
	return ok
}

// Empty checks if hash set is empty.
func (hs HashSet) Empty() bool {
	return len(hs) == 0
}

// Add inserts an element into a hash set.
func (hs HashSet) Add(elem string) {
	hs[elem] = struct{}{}
}

// Remove deletes an element from a hash set.
func (hs HashSet) Remove(elem string) {
	delete(hs, elem)
}
