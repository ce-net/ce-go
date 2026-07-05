// Quickstart: a Go ceapp that both provides and consumes a capability over the mesh.
//
// Run against a live local node (`ce start --no-economy`):
//
//	go run ./examples/quickstart
//
// It serves an "echo" responder on the topic `demo/echo` and, in parallel, requests it from
// its own node — demonstrating publish/subscribe, directed request/reply, and the serve loop
// with nothing but the stdlib.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	ce "github.com/ce-net/ce-go"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c := ce.Connect()
	if err := c.WaitReady(ctx, 30*time.Second); err != nil {
		log.Fatalf("node not ready: %v", err)
	}
	self, err := c.NodeID(ctx)
	if err != nil {
		log.Fatalf("status: %v", err)
	}
	fmt.Printf("on the mesh as %s\n", self)

	// Provide: answer every request on demo/echo by echoing its payload back.
	go func() {
		err := c.Serve(ctx, []string{"demo/echo"}, func(m ce.Message) ([]byte, error) {
			fmt.Printf("serve: request from %s: %q\n", m.Sender, m.Text())
			return []byte("echo: " + m.Text()), nil
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("serve stopped: %v", err)
		}
	}()

	// Consume: call our own responder over the mesh (proving the round-trip).
	time.Sleep(500 * time.Millisecond) // let the subscribe land
	reply, err := c.Request(ctx, self, "demo/echo", []byte("hello from go"), 10*time.Second)
	if err != nil {
		log.Fatalf("request: %v", err)
	}
	fmt.Printf("request round-trip reply: %q\n", string(reply))

	fmt.Println("serving demo/echo — Ctrl-C to stop")
	<-ctx.Done()
}
