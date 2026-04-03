package shredstream

import "encoding/binary"

const (
	DataHeaderSize = 0x58

	flagDataComplete = 0x40
	flagLastInSlot   = 0xC0
)

type ParsedShred struct {
	Slot          uint64
	Index         uint32
	Payload       []byte
	BatchComplete bool
	LastInSlot    bool
}

func ParseShred(raw []byte) *ParsedShred {
	if len(raw) < DataHeaderSize {
		return nil
	}

	slot := binary.LittleEndian.Uint64(raw[0x41:])
	index := binary.LittleEndian.Uint32(raw[0x49:])
	flags := raw[0x55]
	size := binary.LittleEndian.Uint16(raw[0x56:])

	payloadLen := int(size) - DataHeaderSize
	if payloadLen < 0 {
		return nil
	}
	if DataHeaderSize+payloadLen > len(raw) {
		return nil
	}

	payload := raw[DataHeaderSize : DataHeaderSize+payloadLen]

	return &ParsedShred{
		Slot:          slot,
		Index:         index,
		Payload:       payload,
		BatchComplete: flags&flagLastInSlot == flagLastInSlot || flags&flagDataComplete == flagDataComplete,
		LastInSlot:    flags&flagLastInSlot == flagLastInSlot,
	}
}
