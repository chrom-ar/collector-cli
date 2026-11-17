package cli

import (
	"fmt"
	"sync"
	"time"
)

// working shows msg on stderr while something slow runs: a spinner on a
// terminal, the line once otherwise, nothing with --quiet. stop clears
// the spinner.
func (a *app) working(msg string) (stop func()) {
	if a.flags.quiet {
		return func() {}
	}
	if !a.env.StderrIsTerminal {
		a.progress("%s", msg)
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		const frames = `|/-\`
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(a.env.Stderr, "\r%c %s", frames[i%len(frames)], msg)
			select {
			case <-done:
				fmt.Fprint(a.env.Stderr, "\r\033[K")
				return
			case <-t.C:
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}
