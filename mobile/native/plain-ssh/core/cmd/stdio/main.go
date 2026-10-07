// Local desktop IPC companion. No listening socket, agent, or cloud credentials.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	plainssh "io.yaver/plainssh"
	"os"
	"sync"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var output sync.Mutex
	var work sync.WaitGroup
	limit := make(chan struct{}, 32)
	for scanner.Scan() {
		var req struct {
			ID      string          `json:"id"`
			Request json.RawMessage `json:"request"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		limit <- struct{}{}
		work.Add(1)
		go func(id string, body string) {
			defer work.Done()
			defer func() { <-limit }()
			answer := json.RawMessage(plainssh.Invoke(body))
			b, _ := json.Marshal(map[string]any{"id": id, "response": answer})
			output.Lock()
			fmt.Println(string(b))
			output.Unlock()
		}(req.ID, string(req.Request))
	}
	plainssh.Invoke(`{"op":"closeAll"}`)
	work.Wait()
}
