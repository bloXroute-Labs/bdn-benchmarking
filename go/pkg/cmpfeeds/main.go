package cmpfeeds

import (
	"context"
	"fmt"
	"log"
	"time"

	fiber "github.com/chainbound/fiber-go"
)

func main() {
	client := fiber.NewClient("beta.fiberapi.io:8080", "f696bde7-468f-49ed-b588-b41ea06d75bc")
	// Close the client when you're done to make sure API accounting is done correctly
	defer client.Close()

	// Configure a timeout for establishing connection
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	// First make a sink channel on which to receive the transactions
	ch := make(chan *fiber.Transaction)
	go func() {
		// This is a blocking call, so it needs to run in a Goroutine
		if err := client.SubscribeNewTxs(nil, ch); err != nil {
			log.Fatal(err)
		}
	}()

	// Listen for incoming transactions
	for tx := range ch {
		fmt.Printf("%v\n", tx.Hash.String())
	}
}
