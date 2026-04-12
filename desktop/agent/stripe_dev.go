package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// StripeDevStatus holds the current state of the stripe listen process.
type StripeDevStatus struct {
	Listening       bool   `json:"listening"`
	ForwardURL      string `json:"forwardUrl"`
	WebhookSecret   string `json:"webhookSecret"` // whsec_... extracted from stripe listen output
	EventsForwarded int    `json:"eventsForwarded"`
	Port            int    `json:"port"`
	Path            string `json:"path"`
}

// StripeEvent represents a single event from `stripe events list`.
type StripeEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created string `json:"created"`
	Status  string `json:"status"`
}

// ringBuffer is a fixed-size circular log buffer.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	pos   int
	size  int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{lines: make([]string, size), size: size}
}

func (r *ringBuffer) write(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.pos%r.size] = line
	r.pos++
}

func (r *ringBuffer) read() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos == 0 {
		return nil
	}
	start := 0
	count := r.pos
	if count > r.size {
		start = r.pos % r.size
		count = r.size
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, r.lines[(start+i)%r.size])
	}
	return out
}

// StripeDevManager wraps the Stripe CLI for local webhook testing.
type StripeDevManager struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	logBuf          *ringBuffer
	webhookSecret   string
	eventsForwarded int
	port            int
	path            string
}

// NewStripeDevManager creates a new StripeDevManager.
func NewStripeDevManager() *StripeDevManager {
	return &StripeDevManager{
		logBuf: newRingBuffer(100),
	}
}

// CommonStripeEvents lists events commonly used during local development.
var CommonStripeEvents = []string{
	"payment_intent.succeeded",
	"checkout.session.completed",
	"customer.subscription.created",
	"invoice.paid",
}

// Listen starts `stripe listen --forward-to localhost:{port}{path}` in the background.
// An optional events filter may be provided; if nil or empty, all events are forwarded.
func (m *StripeDevManager) Listen(port int, path string, events []string) error {
	if _, err := exec.LookPath("stripe"); err != nil {
		return fmt.Errorf("stripe CLI not found: install it with `brew install stripe/stripe-cli/stripe`")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return fmt.Errorf("stripe listener is already running (call Stop() first)")
	}

	forwardTo := fmt.Sprintf("localhost:%d%s", port, path)
	args := []string{"listen", "--forward-to", forwardTo}
	if len(events) > 0 {
		args = append(args, "--events", strings.Join(events, ","))
	}

	cmd := exec.Command("stripe", args...)

	// Pipe both stdout and stderr through our log buffer so we can extract the
	// webhook signing secret that stripe prints on startup.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return fmt.Errorf("failed to start stripe listen: %w", err)
	}

	m.cmd = cmd
	m.port = port
	m.path = path
	m.webhookSecret = ""
	m.eventsForwarded = 0

	// Background goroutine: read lines, store in ring buffer, extract webhook secret.
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			m.logBuf.write(line)

			// stripe prints the signing secret on a line like:
			//   Ready! Your webhook signing secret is whsec_xxxxxxxxxxxx (^C to quit)
			if strings.Contains(line, "whsec_") {
				for _, field := range strings.Fields(line) {
					if strings.HasPrefix(field, "whsec_") {
						// Strip trailing punctuation such as "(^C" etc.
						secret := strings.TrimRight(field, ",(")
						m.mu.Lock()
						m.webhookSecret = secret
						m.mu.Unlock()
					}
				}
			}

			// Count forwarded events (stripe prints lines like "--> payment_intent.succeeded [200]").
			if strings.HasPrefix(line, "-->") {
				m.mu.Lock()
				m.eventsForwarded++
				m.mu.Unlock()
			}
		}
		// Drain the write-end when the process exits.
		_ = pw.Close()
		_ = pr.Close()
	}()

	// Reap the child process to avoid zombies.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		m.mu.Unlock()
	}()

	return nil
}

// Stop terminates the running stripe listen process.
func (m *StripeDevManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil {
		return fmt.Errorf("stripe listener is not running")
	}

	if err := m.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to stop stripe listener: %w", err)
	}

	m.cmd = nil
	return nil
}

// Trigger runs `stripe trigger {event}` and returns combined stdout/stderr.
func (m *StripeDevManager) Trigger(event string) (string, error) {
	if _, err := exec.LookPath("stripe"); err != nil {
		return "", fmt.Errorf("stripe CLI not found: install it with `brew install stripe/stripe-cli/stripe`")
	}

	var out bytes.Buffer
	cmd := exec.Command("stripe", "trigger", event)
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("stripe trigger %s failed: %w\n%s", event, err, out.String())
	}

	return out.String(), nil
}

// stripeEventsListResponse is the top-level JSON returned by `stripe events list --output json`.
type stripeEventsListResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Created int64  `json:"created"`
		Request struct {
			ID string `json:"id"`
		} `json:"request"`
	} `json:"data"`
}

// Events runs `stripe events list --limit {limit}` and returns parsed events.
func (m *StripeDevManager) Events(limit int) ([]StripeEvent, error) {
	if _, err := exec.LookPath("stripe"); err != nil {
		return nil, fmt.Errorf("stripe CLI not found: install it with `brew install stripe/stripe-cli/stripe`")
	}

	if limit <= 0 {
		limit = 10
	}

	var out bytes.Buffer
	cmd := exec.Command("stripe", "events", "list",
		"--limit", fmt.Sprintf("%d", limit),
		"--output", "json",
	)
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("stripe events list failed: %w\n%s", err, out.String())
	}

	var resp stripeEventsListResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse stripe events list output: %w", err)
	}

	events := make([]StripeEvent, 0, len(resp.Data))
	for _, d := range resp.Data {
		events = append(events, StripeEvent{
			ID:      d.ID,
			Type:    d.Type,
			Created: fmt.Sprintf("%d", d.Created),
			Status:  "succeeded",
		})
	}

	return events, nil
}

// Logs returns recent output captured from the stripe listen process (up to last 100 lines).
func (m *StripeDevManager) Logs() (string, error) {
	lines := m.logBuf.read()
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

// Status returns the current state of the stripe listener.
func (m *StripeDevManager) Status() (*StripeDevStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	listening := m.cmd != nil
	forwardURL := ""
	if listening {
		forwardURL = fmt.Sprintf("http://localhost:%d%s", m.port, m.path)
	}

	return &StripeDevStatus{
		Listening:       listening,
		ForwardURL:      forwardURL,
		WebhookSecret:   m.webhookSecret,
		EventsForwarded: m.eventsForwarded,
		Port:            m.port,
		Path:            m.path,
	}, nil
}
