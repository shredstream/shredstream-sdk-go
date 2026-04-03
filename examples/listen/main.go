package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	shredstream "github.com/shredstream/shredstream-sdk-go"
)

func main() {
	listener, err := shredstream.NewListener(8001)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind failed: %v\n", err)
		os.Exit(1)
	}

	listener.OnTransactions(func(slot uint64, txs []shredstream.Transaction) {
		for _, tx := range txs {
			fmt.Println(tx.Signature())
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintln(os.Stderr, "Listening on port 8001...")
	listener.Start(ctx)
}
