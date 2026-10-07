package plainssh

import (
	"io"
	"sync"
	"time"
)

// Enrollment is a separate SSH exec channel. Never type account setup into
// the user's runner pane, and never copy the phone's Yaver bearer to the box.
type enrollment struct {
	mu     sync.Mutex
	output []byte
	done   bool
	err    string
}

func (e *enrollment) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.output = append(e.output, p...)
	if len(e.output) > 65536 {
		e.output = e.output[len(e.output)-65536:]
	}
	return len(p), nil
}
func (c *connection) enroll(install bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enrollment != nil {
		c.enrollment.mu.Lock()
		done := c.enrollment.done
		c.enrollment.mu.Unlock()
		if !done {
			return fail("SSH_ENROLLMENT_BUSY", "Remote Yaver setup is already running.")
		}
	}
	s, err := c.client.NewSession()
	if err != nil {
		return err
	}
	e := &enrollment{}
	s.Stdout = e
	s.Stderr = e
	command := tmuxPath + `command -v yaver >/dev/null || { printf 'Yaver is not installed. Choose Install Yaver to continue.\n'; exit 127; }; YAVER_NO_QR=1 yaver auth --headless`
	if install {
		command = tmuxPath + `command -v npm >/dev/null || { printf 'Node.js/npm is missing. Install Node.js on this host, then retry.\n'; exit 127; }; npm install -g yaver-cli`
	}
	if err = s.Start(command); err != nil {
		s.Close()
		return err
	}
	c.enrollment = e
	go func() {
		timer := time.AfterFunc(15*time.Minute, func() { s.Close() })
		defer timer.Stop()
		err := s.Wait()
		s.Close()
		e.mu.Lock()
		defer e.mu.Unlock()
		e.done = true
		if err != nil {
			e.err = "Remote setup did not complete. Review its output and retry."
		}
	}()
	return nil
}
func (c *connection) enrollmentRead() (any, error) {
	c.mu.Lock()
	e := c.enrollment
	c.mu.Unlock()
	if e == nil {
		return nil, fail("SSH_ENROLLMENT_IDLE", "Start remote Yaver sign-in first.")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := string(e.output)
	e.output = nil
	return map[string]any{"output": out, "done": e.done, "error": e.err}, nil
}

var _ io.Writer = (*enrollment)(nil)
