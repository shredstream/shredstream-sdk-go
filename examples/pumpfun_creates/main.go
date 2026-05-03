package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mr-tron/base58"
	shredstream "github.com/shredstream/shredstream-sdk-go/v2"
)

const pumpfunProgramID = "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"

var (
	createDisc   = []byte{24, 30, 200, 40, 5, 28, 7, 119}
	createV2Disc = []byte{214, 144, 76, 236, 95, 139, 49, 180}
)

type pumpfunCreate struct {
	mint         string
	bondingCurve string
	creator      string
}

func detectCreate(tx shredstream.VersionedTransaction, programID []byte) *pumpfunCreate {
	keys := tx.Message.AccountKeys
	for _, ix := range tx.Message.Instructions {
		if int(ix.ProgramIDIndex) >= len(keys) {
			continue
		}
		pid := keys[ix.ProgramIDIndex]
		if !bytes.Equal(pid[:], programID) {
			continue
		}
		if len(ix.Data) < 8 {
			continue
		}
		disc := ix.Data[:8]
		isCreate := bytes.Equal(disc, createDisc)
		isV2 := bytes.Equal(disc, createV2Disc)
		if !isCreate && !isV2 {
			continue
		}
		resolve := func(idx int) string {
			if idx >= len(ix.Accounts) {
				return ""
			}
			k := int(ix.Accounts[idx])
			if k >= len(keys) {
				return ""
			}
			return base58.Encode(keys[k][:])
		}
		creatorIdx := 7
		if isV2 {
			creatorIdx = 5
		}
		return &pumpfunCreate{
			mint:         resolve(0),
			bondingCurve: resolve(2),
			creator:      resolve(creatorIdx),
		}
	}
	return nil
}

func printCard(slot uint64, sig string, c *pumpfunCreate) {
	now := time.Now()
	timeStr := fmt.Sprintf("%02d:%02d:%02d.%03d",
		now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1_000_000)

	sigShort := sig
	if len(sig) >= 8 {
		sigShort = sig[:4] + "..." + sig[len(sig)-4:]
	}

	const (
		G   = "\x1b[1;32m"
		DIM = "\x1b[90m"
		W   = "\x1b[97m"
		Y   = "\x1b[33m"
		C   = "\x1b[36m"
		M   = "\x1b[35m"
		D   = "\x1b[2m"
		R   = "\x1b[0m"
	)

	fmt.Printf("%s┌───────────────────────────────────────────────────────────────┐%s\n", DIM, R)
	fmt.Printf("%s│%s  🌐 %sShredStream.com%s %sSDK%s                                       %s│%s\n", DIM, R, W, R, DIM, R, DIM, R)
	fmt.Printf("%s└───────────────────────────────────────────────────────────────┘%s\n", DIM, R)
	fmt.Println()
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━ 🚀 PUMPFUN CREATE ━━━━━━━━━━━━━━━━━━━━━━━%s\n", G, R)
	fmt.Printf(" %s›%s %s🕐 Time%s     %s%s%s\n", DIM, R, DIM, R, W, timeStr, R)
	fmt.Printf(" %s›%s %s📦 Slot%s     %s%d%s\n", DIM, R, DIM, R, W, slot, R)
	fmt.Printf(" %s›%s %s🪙 Mint%s     %s%s%s\n", DIM, R, DIM, R, Y, c.mint, R)
	fmt.Printf(" %s›%s %s📈 Curve%s    %s%s%s\n", DIM, R, DIM, R, C, c.bondingCurve, R)
	fmt.Printf(" %s›%s %s👤 Creator%s  %s%s%s\n", DIM, R, DIM, R, M, c.creator, R)
	fmt.Printf(" %s›%s %s🔑 Sig%s      %s%s%s\n", DIM, R, DIM, R, D, sigShort, R)
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", G, R)
}

func main() {
	port := 8001
	if len(os.Args) > 1 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil {
			port = p
		}
	}

	programIDBytes, err := base58.Decode(pumpfunProgramID)
	if err != nil {
		log.Fatalf("decode program id: %v", err)
	}

	listener, err := shredstream.Bind(port)
	if err != nil {
		log.Fatalf("bind: %v", err)
	}
	defer listener.Close()

	fmt.Fprintf(os.Stderr, "Listening for PumpFun creates on 0.0.0.0:%d\n", port)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var found uint64
	iter := listener.Transactions(ctx)
	for iter.Next() {
		slot, txs := iter.Slot(), iter.Txs()
		for _, tx := range txs {
			c := detectCreate(tx, programIDBytes)
			if c == nil {
				continue
			}
			found++
			sig := base58.Encode(tx.Signatures[0][:])
			fmt.Print("\x1b[H\x1b[2J")
			printCard(slot, sig, c)
			fmt.Printf("\n\x1b[90m  #%d detected\x1b[0m\n", found)
		}
	}
}
