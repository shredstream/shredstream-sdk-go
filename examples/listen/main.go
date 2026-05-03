package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	shredstream "github.com/shredstream/shredstream-sdk-go/v2"
)

func main() {
	port := 8001
	if v := os.Getenv("SHREDSTREAM_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	listener, err := shredstream.Bind(port)
	if err != nil {
		log.Fatalf("bind: %v", err)
	}
	defer listener.Close()

	addr, _ := listener.LocalAddr()
	log.Printf("listening on %s", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	iter := listener.Transactions(ctx)
	for iter.Next() {
		slot, txs := iter.Slot(), iter.Txs()
		for _, tx := range txs {
			if len(tx.Signatures) > 0 {
				fmt.Printf("slot=%d sig=%s\n", slot, hex.EncodeToString(tx.Signatures[0][:]))
			}
		}
	}
}
