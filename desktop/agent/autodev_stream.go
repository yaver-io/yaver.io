package main

// autodev_stream.go — bridges a long-running CLI command's stdout/stderr
// to the local daemon's named log-stream so the mobile app and web
// dashboard can watch the run in real time, while the original
// terminal still sees everything as before.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// streamPublisher batches log lines and POSTs them to the local
// daemon's /streams/{name}/append endpoint. It is intentionally
// best-effort: if the daemon is unreachable, drops are silent and
// the CLI keeps running. We never block the producer.
type streamPublisher struct {
	endpoint string
	token    string
	in       chan string
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func newStreamPublisher(name string) *streamPublisher {
	cfg, _ := LoadConfig()
	p := &streamPublisher{
		endpoint: fmt.Sprintf("http://127.0.0.1:18080/streams/%s/append", name),
		token:    cfg.AuthToken,
		in:       make(chan string, 1024),
	}
	p.wg.Add(1)
	go p.run()
	return p
}

func (p *streamPublisher) Publish(line string) {
	if p == nil {
		return
	}
	select {
	case p.in <- line:
	default: // buffer full → drop; CLI must never stall on streaming
	}
}

func (p *streamPublisher) Close() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		close(p.in)
	})
	p.wg.Wait()
}

func (p *streamPublisher) run() {
	defer p.wg.Done()
	client := &http.Client{Timeout: 2 * time.Second}
	flushTicker := time.NewTicker(150 * time.Millisecond)
	defer flushTicker.Stop()

	var batch []string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		body, _ := json.Marshal(map[string]interface{}{"lines": batch})
		req, err := http.NewRequest("POST", p.endpoint, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if p.token != "" {
				req.Header.Set("Authorization", "Bearer "+p.token)
			}
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case line, ok := <-p.in:
			if !ok {
				flush()
				return
			}
			batch = append(batch, line)
			if len(batch) >= 64 {
				flush()
			}
		case <-flushTicker.C:
			flush()
		}
	}
}

// teeStdoutToStream redirects os.Stdout and os.Stderr through a pair
// of pipes so every line written is also published to the named log
// stream on the local daemon. The original stdout/stderr keep
// receiving the same bytes — terminal UX is unchanged. Subprocesses
// started after this call inherit the piped FDs, so their output is
// streamed too. Returns a cleanup func that restores the originals
// and waits for the publisher to drain.
//
// If anything fails to set up (e.g. pipe creation), this is a no-op
// and the returned cleanup is harmless.
func teeStdoutToStream(streamName string) func() {
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, errOut := os.Pipe()
	if errOut != nil {
		return func() {}
	}
	rErr, wErr, errErr := os.Pipe()
	if errErr != nil {
		rOut.Close()
		wOut.Close()
		return func() {}
	}

	publisher := newStreamPublisher(streamName)

	// Open a per-stream log file under /tmp/yaver/<stream>.log so
	// the user (or another agent) can `tail -f` the run after the
	// fact. Best-effort: a file-open failure does NOT degrade the
	// terminal or daemon-stream output paths.
	var logFile *os.File
	if dir := "/tmp/yaver"; os.MkdirAll(dir, 0o755) == nil {
		safeName := strings.ReplaceAll(streamName, ":", "_")
		path := dir + "/" + safeName + ".log"
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			fmt.Fprintf(os.Stderr, "[stream] tee -> %s\n", path)
		}
	}

	// Announce the run so subscribers that connect mid-stream see
	// when it started.
	publisher.Publish(fmt.Sprintf("─── %s started %s ───",
		streamName, time.Now().Format(time.RFC3339)))

	os.Stdout, os.Stderr = wOut, wErr

	var pumpWG sync.WaitGroup
	// pump copies bytes through immediately (so the terminal sees
	// every chunk the subprocess emits, including partial-line tokens
	// from Claude's streaming output) while accumulating lines on the
	// side for daemon-stream publishing. No bufio.Scanner — that
	// would hold writes until the next \n and ruin live UX.
	pump := func(r *os.File, dst io.Writer) {
		defer pumpWG.Done()
		buf := make([]byte, 4096)
		var lineBuf bytes.Buffer
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				_, _ = dst.Write(chunk) // raw passthrough — instant
				if logFile != nil {
					_, _ = logFile.Write(chunk)
				}
				lineBuf.Write(chunk)
				for {
					data := lineBuf.Bytes()
					idx := bytes.IndexByte(data, '\n')
					if idx < 0 {
						break
					}
					line := strings.TrimRight(string(data[:idx]), "\r")
					publisher.Publish(line)
					lineBuf.Next(idx + 1)
				}
			}
			if err != nil {
				// Flush any trailing partial line so subscribers see
				// it before the stream closes.
				if rest := strings.TrimRight(lineBuf.String(), "\r\n"); rest != "" {
					publisher.Publish(rest)
				}
				return
			}
		}
	}
	pumpWG.Add(2)
	go pump(rOut, origOut)
	go pump(rErr, origErr)

	return func() {
		// Restore first so any further writes from this process
		// (e.g. from a deferred recover) go to the real terminal.
		os.Stdout, os.Stderr = origOut, origErr
		// Closing the write ends causes the pump scanners to EOF.
		wOut.Close()
		wErr.Close()
		pumpWG.Wait()
		rOut.Close()
		rErr.Close()
		publisher.Publish(fmt.Sprintf("─── %s ended %s ───",
			streamName, time.Now().Format(time.RFC3339)))
		publisher.Close()
		if logFile != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("─── %s ended %s ───\n",
				streamName, time.Now().Format(time.RFC3339)))
			_ = logFile.Close()
		}
	}
}
