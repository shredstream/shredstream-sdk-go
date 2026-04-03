# Solana ShredStream SDK for Go

Solana ShredStream SDK/Decoder for Go, enabling ultra-low latency Solana transaction streaming via UDP shreds from ShredStream.com

> Part of the [ShredStream.com](https://shredstream.com) ecosystem — ultra-low latency [Solana shred streaming](https://shredstream.com) via UDP.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)](#)

## 📋 Prerequisites

1. **Create an account** on [ShredStream.com](https://shredstream.com)
2. **Launch a Shred Stream** and pick your region (Frankfurt, Amsterdam, Singapore, Chicago, and more)
3. **Enter your server's IP address** and the UDP port where you want to receive shreds
4. **Open your firewall** for inbound UDP traffic on that port (e.g. configure your cloud provider's security group)
5. Install [Go 1.21+](https://go.dev/dl/):
   ```bash
   # Linux (amd64)
   wget https://go.dev/dl/go1.24.2.linux-amd64.tar.gz
   sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.2.linux-amd64.tar.gz
   export PATH=$PATH:/usr/local/go/bin

   # macOS
   brew install go
   ```

> 🎁 Want to try before you buy? Open a ticket on our [Discord](https://discord.gg/4w2DNbTaWD) to request a free trial.

## 📦 Installation

```bash
# Initialize your project (skip if you already have a go.mod)
go mod init myproject

# Install the SDK
go get github.com/shredstream/shredstream-sdk-go
```

## ⚡ Quick Start

Create a file `main.go`:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "strconv"

    shredstream "github.com/shredstream/shredstream-sdk-go"
)

func main() {
    port, _ := strconv.Atoi(os.Getenv("SHREDSTREAM_PORT"))
    if port == 0 { port = 8001 }

    listener, err := shredstream.NewListener(port)
    if err != nil {
        log.Fatal(err)
    }

    // Decoded transactions — ready-to-use Solana transactions
    listener.OnTransactions(func(slot uint64, txs []shredstream.Transaction) {
        for _, tx := range txs {
            fmt.Printf("slot %d: %s\n", slot, tx.Signature())
        }
    })

    // OR raw shreds — lowest latency, arrives before block assembly
    // listener.OnShred(func(slot uint64, index uint32, payload []byte) {
    //     fmt.Printf("slot %d index %d len %d\n", slot, index, len(payload))
    // })

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    if err := listener.Start(ctx); err != nil {
        log.Fatal(err)
    }
}
```

Run it:

```bash
go run main.go
```

## 📖 API Reference

### `NewListener(port int) (*ShredListener, error)`

Creates a listener with default options (25 MB recv buf, 10 slot max age).

### `NewListenerWithOptions(port int, opts ...ListenerOption) (*ShredListener, error)`

| Option | Function | Default | Description |
|--------|----------|---------|-------------|
| `WithRecvBuf(size)` | `ListenerOption` | 25 MB | UDP receive buffer size |
| `WithMaxAge(slots)` | `ListenerOption` | 10 | Maximum slot age before eviction |

#### Methods

- `listener.OnTransactions(func(uint64, []Transaction))` -- Register callback for decoded transactions
- `listener.OnShred(func(uint64, uint32, []byte))` -- Register callback for raw shreds (slot, index, payload)
- `listener.Start(ctx context.Context) error` -- Start listening (blocks until context cancelled)
- `listener.Stop()` -- Stop the listener
- `listener.ActiveSlots() int` -- Number of slots currently being accumulated

### `Transaction`

| Field | Type | Description |
|-------|------|-------------|
| `Signatures` | `[][]byte` | Raw 64-byte signatures |
| `Raw` | `[]byte` | Full wire-format transaction bytes |

Methods:
- `tx.Signature() string` -- First signature as base58

## 🎯 Use Cases

ShredStream.com shred data powers a wide range of latency-sensitive strategies — HFT, MEV extraction, token sniping, copy trading, liquidation bots, on-chain analytics, and more.

### 💎 PumpFun Token Sniping

ShredStream.com SDK detects PumpFun token creations **~499ms before they appear on PumpFun's live feed** — tested across 25 consecutive detections:

<img src="https://raw.githubusercontent.com/shredstream/shredstream-sdk-go/main/assets/shredstream.com_sdk_vs_pumpfun_live_feed.gif" alt="ShredStream.com SDK vs PumpFun live feed — ~499ms advantage" width="600">

> [ShredStream.com](https://shredstream.com) provides a complete, optimized PumpFun token creation detection code exclusively to Pro plan subscribers and above. Battle-tested, high-performance, ready to plug into your sniping pipeline. To get access, open a ticket on [Discord](https://discord.gg/4w2DNbTaWD) or reach out on Telegram [@shredstream](https://t.me/shredstream).

## ⚙️ Configuration

### OS Tuning

```bash
# Linux — increase max receive buffer
sudo sysctl -w net.core.rmem_max=33554432

# macOS
sudo sysctl -w kern.ipc.maxsockbuf=33554432
```

## 🚀 Launch a Shred Stream

Need a feed? **[Launch a Solana Shred Stream on ShredStream.com](https://shredstream.com)** — sub-millisecond delivery, multiple global regions, 5-minute setup.

## 🔗 Links

- 🌐 Website: https://www.shredstream.com/
- 📖 Documentation: https://docs.shredstream.com/
- 🐦 X (Twitter): https://x.com/ShredStream
- 🎮 Discord: https://discord.gg/4w2DNbTaWD
- 💬 Telegram: https://t.me/ShredStream
- 💻 GitHub: https://github.com/ShredStream
- 🎫 Support: [Discord](https://discord.gg/4w2DNbTaWD)
- 📊 Benchmarks: [Discord](https://discord.gg/4w2DNbTaWD)

## 📄 License

MIT — [ShredStream.com](https://shredstream.com)
