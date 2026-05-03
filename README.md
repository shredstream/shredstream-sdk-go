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
go get github.com/shredstream/shredstream-sdk-go/v2
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
    "strconv"

    shredstream "github.com/shredstream/shredstream-sdk-go/v2"
)

func main() {
    port, _ := strconv.Atoi(os.Getenv("SHREDSTREAM_PORT"))
    if port == 0 { port = 8001 }

    listener, err := shredstream.Bind(port)
    if err != nil { log.Fatal(err) }
    defer listener.Close()

    iter := listener.Transactions(context.Background())
    for iter.Next() {
        slot, txs := iter.Slot(), iter.Txs()
        for _, tx := range txs {
            fmt.Printf("slot %d: %x\n", slot, tx.Signatures[0])
        }
    }
}
```

Run it:

```bash
go run main.go
```

## 📖 API Reference

### `Listener`

- `shredstream.Bind(port int) (*Listener, error)` — Bind with defaults (64 MB recv buf, 3 slot window, FEC enabled)
- `shredstream.BindWithOptions(port int, opts ListenerOptions) (*Listener, error)` — Custom configuration
- `shredstream.Offline() *Listener` / `OfflineWithOptions(opts) *Listener` — No socket; drive via `HandlePacket` (replay/tests)
- `shredstream.FromConn(conn net.PacketConn, opts) *Listener` — Adopt an existing connection
- `listener.Transactions(ctx) *TransactionIter` — Blocking iterator yielding `(slot, []VersionedTransaction)`
- `listener.Shreds(ctx) *ShredIter` — Iterator of raw shred headers (no decode)
- `listener.HandlePacket(raw []byte) (uint64, []VersionedTransaction, bool)` — Inject an externally-received UDP datagram
- `listener.LocalAddr() (net.Addr, error)` — Bound socket address
- `listener.SetReadDeadline(t time.Time) error` — Forward to underlying socket
- `listener.Close() error` — Release the socket and pool

### `ListenerOptions`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `RecvBuf` | `int` | `64 MB` | `SO_RCVBUF` size |
| `MaxAge` | `int` | `3` | Slot retention window |
| `BusyPollMicros` | `uint32` | `200` | Linux `SO_BUSY_POLL` µs (0 disables) |
| `PoolSize` | `int` | `4096` | Number of 2 KiB buffers in the zero-copy pool |
| `EnableFEC` | `bool` | `true` | Reed-Solomon recovery on dropped data shreds |
| `DisableSalvageDelivery` | `bool` | `false` | Drop salvaged tail txs for lowest p99 |
| `Accumulator` | `AccumulatorConfig` | *defaults* | FEC and stuck-batch tuning |

`shredstream.DefaultListenerOptions()` returns the defaults above.

### `AccumulatorConfig`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxFECSetsPerSlot` | `int` | `32` | Per-slot FEC buffer cap |
| `StuckBatchTimeout` | `time.Duration` | `50ms` | Force-finalize a stuck batch after this delay |

### Metrics

Lock-free atomic counters on `*Listener`:

| Group | Methods |
|-------|---------|
| **Throughput** | `DataShredCountTotal`, `CodeShredCountTotal`, `BytesReceived`, `SlotCount` |
| **Decoder** | `BatchesDecodedStreamingTotal`, `BatchesDecodedFallbackTotal`, `BatchesSkippedTotal`, `DecodeErrorsTotal` |
| **FEC** | `FECRecoveriesTotal`, `FECRecoveryFailuresTotal`, `FECSetsDiscardedUnusedTotal`, `FECSetsEvictedEarlyTotal` |
| **Unparseable** | `UnparseablePackets`, `UnparseableTooShort`, `UnparseableVariant`, `UnparseablePayload`, `UnparseableSlotRange` |
| **Slot lifecycle** | `SlotsCompletedTotal`, `SlotsEvictedByAge`, `DroppedKnownSlots`, `HarvestedBatchesTotal`, `SalvagedTailTxTotal` |
| **Tail control** | `BatchesForceFinalizedCorruptedTotal`, `BatchesForceFinalizedTimeoutTotal` |
| **Pool / I-O** | `PoolExhaustedCount`, `LastIOErrorKind`, `BusyPollActive` |

### Helpers

- `shredstream.ClassifyVariant(b byte) (VariantKind, bool)` — Classify a shred variant byte
- `shredstream.PinThreadToCPU(cpu int) error` — Pin the calling goroutine. Pair with `runtime.LockOSThread()`. Linux: `sched_setaffinity`; macOS: hint; other: no-op
- `shredstream.LockOSThread()` — Convenience wrapper around `runtime.LockOSThread`

## 🎯 Use Cases

ShredStream.com shred data powers a wide range of latency-sensitive strategies — HFT, MEV extraction, token sniping, copy trading, liquidation bots, on-chain analytics, and more.

### 💎 PumpFun Token Sniping

ShredStream.com SDK detects PumpFun token creations **~499ms before they appear on PumpFun's live feed** — tested across 25 consecutive detections:

<img src="https://raw.githubusercontent.com/shredstream/shredstream-sdk-go/main/assets/shredstream.com_sdk_vs_pumpfun_live_feed.gif" alt="ShredStream.com SDK vs PumpFun live feed — ~499ms advantage" width="600">

> Ready-to-run example included: see [`examples/pumpfun_creates`](examples/pumpfun_creates). Run with `go run ./examples/pumpfun_creates [port]`.

## ⚙️ Configuration

### OS Tuning

For high-throughput environments, increase the kernel receive buffer:

```bash
# Linux
sudo sysctl -w net.core.rmem_max=67108864
sudo sysctl -w net.core.busy_read=200

# macOS
sudo sysctl -w kern.ipc.maxsockbuf=67108864
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
