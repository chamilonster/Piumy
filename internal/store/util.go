package store

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullIfEmpty maps "" to a real SQL NULL so a model/confirmer column stays
// unset (not "") when the caller never passed one.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
