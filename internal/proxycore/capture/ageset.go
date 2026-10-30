package capture

// ageSet remembers recently used keys in two generations. rotate drops keys
// that were not used since the previous rotate, so the set stays bounded by
// the traffic of the last two intervals instead of growing forever.
type ageSet[K comparable] struct {
	cur map[K]struct{}
	old map[K]struct{}
}

func newAgeSet[K comparable]() ageSet[K] {
	return ageSet[K]{cur: map[K]struct{}{}, old: map[K]struct{}{}}
}

// has reports k and keeps it alive for another generation.
func (s *ageSet[K]) has(k K) bool {
	if _, ok := s.cur[k]; ok {
		return true
	}
	if _, ok := s.old[k]; ok {
		delete(s.old, k)
		s.cur[k] = struct{}{}
		return true
	}
	return false
}

func (s *ageSet[K]) add(k K) { s.cur[k] = struct{}{} }

func (s *ageSet[K]) del(k K) {
	delete(s.cur, k)
	delete(s.old, k)
}

func (s *ageSet[K]) rotate() {
	s.old = s.cur
	s.cur = map[K]struct{}{}
}

func (s *ageSet[K]) len() int { return len(s.cur) + len(s.old) }
