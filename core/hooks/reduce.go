package hooks

// StopWhen is the veto shape: the first handler whose result satisfies pred wins
// and the rest are skipped. Results that fail pred are discarded.
func StopWhen[E any, R any](pred func(R) bool) Reducer[E, R] {
	return func(acc *R, _ *E, out R) bool {
		if !pred(out) {
			return false
		}
		*acc = out
		return true
	}
}

// Fold is the mutation shape: apply merges each result into both the accumulator
// and the event, so the next handler sees what the previous one changed. It never
// short-circuits — every handler gets a turn.
func Fold[E any, R any](apply func(acc *R, ev *E, out R)) Reducer[E, R] {
	return func(acc *R, ev *E, out R) bool {
		apply(acc, ev, out)
		return false
	}
}
