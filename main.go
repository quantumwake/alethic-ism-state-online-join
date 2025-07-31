package main

import (
	"alethic-ism-state-online-join/pkg/handler"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	handler.Startup(ctx)

	defer func() {
		cancel()
		log.Println("Received termination signal")
		handler.Teardown(ctx)
	}()

	// set up process signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan // wait on signal

	log.Println("shut down")
}
