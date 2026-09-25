package proc

import "io"

func pumpOutput(r io.Reader, buf *OutputBuffer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				_, _ = buf.Write(tmp[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return done
}
