// wstail — test client for the relay's /tail WebSocket. Mirrors what Claude
// Code's Monitor {ws:} source does: offer the bearer subprotocol, require its
// echo, print each text frame as a line, answer pings.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"claudebus/relay/internal/wire"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "relay host:port")
	channel := flag.String("channel", "", "channel")
	alias := flag.String("alias", "", "alias")
	token := flag.String("token", "", "app token")
	count := flag.Int("n", 0, "exit after N messages (0 = run forever)")
	timeout := flag.Duration("timeout", 0, "exit after this long (0 = none)")
	flag.Parse()
	if *channel == "" || *alias == "" || *token == "" {
		log.Fatal("need -channel, -alias, -token")
	}

	path := fmt.Sprintf("/tail?channel=%s&alias=%s",
		url.QueryEscape(*channel), url.QueryEscape(*alias))
	conn, err := wire.Dial(*addr, path, "bearer.cbus."+*token, 5*time.Second)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(os.Stderr, "connected (subprotocol echoed)")

	deadline := time.Time{}
	if *timeout > 0 {
		deadline = time.Now().Add(*timeout)
	}
	got := 0
	for {
		if !deadline.IsZero() {
			if time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "timeout reached")
				return
			}
			_ = conn.SetReadDeadline(deadline)
		}
		op, payload, err := conn.ReadFrame()
		if err != nil {
			if !deadline.IsZero() && time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "timeout reached")
				return
			}
			log.Fatalf("read: %v", err)
		}
		switch op {
		case wire.OpText:
			fmt.Println(string(payload))
			got++
			if *count > 0 && got >= *count {
				return
			}
		case wire.OpPing:
			_ = conn.WriteFrame(wire.OpPong, payload)
		case wire.OpClose:
			fmt.Fprintln(os.Stderr, "server closed")
			return
		}
	}
}
