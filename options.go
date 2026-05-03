package shredstream

import (
	"errors"
	"fmt"
	"time"
)

const Version = "2.0.0"

const (
	DefaultRecvBuf           = 64 * 1024 * 1024
	DefaultMaxAge            = 3
	DefaultBusyPollMicros    = 200
	DefaultPoolSize          = 4096
	DefaultMaxFECSetsPerSlot = 32
	DefaultStuckBatchTimeout = 50 * time.Millisecond
)

type AccumulatorConfig struct {
	MaxFECSetsPerSlot int
	StuckBatchTimeout time.Duration
}

func DefaultAccumulatorConfig() AccumulatorConfig {
	return AccumulatorConfig{
		MaxFECSetsPerSlot: DefaultMaxFECSetsPerSlot,
		StuckBatchTimeout: DefaultStuckBatchTimeout,
	}
}

type ListenerOptions struct {
	RecvBuf                int
	MaxAge                 int
	BusyPollMicros         uint32
	PoolSize               int
	EnableFEC              bool
	DisableSalvageDelivery bool
	Accumulator            AccumulatorConfig
}

var ErrInvalidOptions = errors.New("shredstream: invalid ListenerOptions")

func (o ListenerOptions) validate() error {
	if o.PoolSize <= 0 || o.PoolSize > maxPoolCapacity {
		return fmt.Errorf("%w: PoolSize=%d must be in [1, %d]",
			ErrInvalidOptions, o.PoolSize, maxPoolCapacity)
	}
	if o.RecvBuf < 0 {
		return fmt.Errorf("%w: RecvBuf=%d must be non-negative",
			ErrInvalidOptions, o.RecvBuf)
	}
	if o.MaxAge == 0 {
		return fmt.Errorf("%w: MaxAge must be > 0", ErrInvalidOptions)
	}
	return nil
}

func DefaultListenerOptions() ListenerOptions {
	return ListenerOptions{
		RecvBuf:                DefaultRecvBuf,
		MaxAge:                 DefaultMaxAge,
		BusyPollMicros:         DefaultBusyPollMicros,
		PoolSize:               DefaultPoolSize,
		EnableFEC:              true,
		DisableSalvageDelivery: false,
		Accumulator:            DefaultAccumulatorConfig(),
	}
}
