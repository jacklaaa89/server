package main

import (
	"context"

	"github.com/jacklaaa89/skybet/server"
)

// main runs the main server using the default config.
func main() {
	server, err := server.New(context.Background(), server.NewDefaultConfig())
	if err != nil {
		panic(err)
	}

	server.Listen()
}
