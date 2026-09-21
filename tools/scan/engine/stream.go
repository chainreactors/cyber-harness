package engine

import "context"

// forwardResults discards results after cancellation but continues draining upstream.
// Close the output only after upstream closes and local cleanup completes.
func forwardResults[T, R any](ctx context.Context, input <-chan T, convert func(T) (R, bool), release func()) <-chan R {
	output := make(chan R)
	go func() {
		defer close(output)
		defer release()
		for value := range input {
			if ctx.Err() != nil {
				continue
			}
			result, ok := convert(value)
			if !ok {
				continue
			}
			select {
			case output <- result:
			case <-ctx.Done():
			}
		}
	}()
	return output
}
