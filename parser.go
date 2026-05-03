package shredstream

import "encoding/binary"

const (
	VariantOffset      = 0x40
	SlotOffset         = 0x41
	IndexOffset        = 0x49
	VersionOffset      = 0x4D
	FecSetIndexOffset  = 0x4F
	DataParentOffset   = 0x53
	DataFlagsOffset    = 0x55
	DataSizeOffset     = 0x56
	DataHeaderSize     = 0x58
	CodeNumDataOffset  = 0x53
	CodeNumCodingOffset = 0x55
	CodePositionOffset = 0x57
	CodeHeaderSize     = 0x59
)

const (
	dataComplete = 0b0100_0000
	lastInSlot   = 0b1100_0000
)

const MaxDataShredSize = 1203

type ShredKind interface {
	isShredKind()
}

type DataShred struct {
	Slot          uint64
	Index         uint32
	Version       uint16
	FecSetIndex   uint32
	ParentOffset  uint16
	Payload       []byte
	BatchComplete bool
	LastInSlot    bool
	Variant       VariantKind
	RawLen        int
}

func (DataShred) isShredKind() {}

type CodeShred struct {
	Slot            uint64
	Index           uint32
	Version         uint16
	FecSetIndex     uint32
	NumDataShreds   uint16
	NumCodingShreds uint16
	Position        uint16
	Coded           []byte
	Variant         VariantKind
}

func (CodeShred) isShredKind() {}

type ParsedShred struct {
	Slot          uint64
	Index         uint32
	Payload       []byte
	BatchComplete bool
	LastInSlot    bool
}

func readU16(raw []byte, offset int) (uint16, bool) {
	end := offset + 2
	if end > len(raw) || offset < 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(raw[offset:end]), true
}

func readU32(raw []byte, offset int) (uint32, bool) {
	end := offset + 4
	if end > len(raw) || offset < 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(raw[offset:end]), true
}

func readU64(raw []byte, offset int) (uint64, bool) {
	end := offset + 8
	if end > len(raw) || offset < 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint64(raw[offset:end]), true
}

func ParseKind(raw []byte) (ShredKind, error) {
	if len(raw) < DataHeaderSize {
		return nil, newParseError(ErrTooShort)
	}
	variantByte := raw[VariantOffset]
	slot, ok := readU64(raw, SlotOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	index, ok := readU32(raw, IndexOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	version, ok := readU16(raw, VersionOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	fecSetIndex, ok := readU32(raw, FecSetIndexOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}

	if kind, classified := ClassifyVariant(variantByte); classified {
		if kind.IsData() {
			return parseDataWithKind(raw, slot, index, version, fecSetIndex, kind)
		}
		if kind.IsCode() {
			return parseCodeWithKind(raw, slot, index, version, fecSetIndex, kind)
		}
	}

	res, err := parseDataLegacyFallback(raw, slot, index)
	if err != nil {
		return nil, newParseError(ErrUnknownVariant)
	}
	return res, nil
}

func parseDataWithKind(raw []byte, slot uint64, index uint32, version uint16, fecSetIndex uint32, variant VariantKind) (ShredKind, error) {
	if fecSetIndex > index {
		return parseDataLegacyFallback(raw, slot, index)
	}
	parentOffset, ok := readU16(raw, DataParentOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	if DataFlagsOffset >= len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	flags := raw[DataFlagsOffset]
	sizeU, ok := readU16(raw, DataSizeOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	size := int(sizeU)
	if size > MaxDataShredSize {
		return parseDataLegacyFallback(raw, slot, index)
	}
	if size > len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	var payload []byte
	if size > DataHeaderSize {
		if size > len(raw) {
			return nil, newParseError(ErrPayloadInvalid)
		}
		payload = raw[DataHeaderSize:size]
	} else {
		payload = []byte{}
	}
	return DataShred{
		Slot:          slot,
		Index:         index,
		Version:       version,
		FecSetIndex:   fecSetIndex,
		ParentOffset:  parentOffset,
		Payload:       payload,
		BatchComplete: flags&dataComplete != 0,
		LastInSlot:    flags&lastInSlot == lastInSlot,
		Variant:       variant,
		RawLen:        len(raw),
	}, nil
}

func parseCodeWithKind(raw []byte, slot uint64, index uint32, version uint16, fecSetIndex uint32, variant VariantKind) (ShredKind, error) {
	if len(raw) < CodeHeaderSize {
		return nil, newParseError(ErrTooShort)
	}
	numData, ok := readU16(raw, CodeNumDataOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	numCoding, ok := readU16(raw, CodeNumCodingOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	position, ok := readU16(raw, CodePositionOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	if numData == 0 || numCoding == 0 || position >= numCoding {
		return nil, newParseError(ErrPayloadInvalid)
	}
	suffix := variant.MerkleSuffix()
	if suffix >= len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	codedEnd := len(raw) - suffix
	if codedEnd < 0 || codedEnd > len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	coded := raw[:codedEnd]
	return CodeShred{
		Slot:            slot,
		Index:           index,
		Version:         version,
		FecSetIndex:     fecSetIndex,
		NumDataShreds:   numData,
		NumCodingShreds: numCoding,
		Position:        position,
		Coded:           coded,
		Variant:         variant,
	}, nil
}

func parseDataLegacyFallback(raw []byte, slot uint64, index uint32) (ShredKind, error) {
	if DataFlagsOffset >= len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	flags := raw[DataFlagsOffset]
	sizeU, ok := readU16(raw, DataSizeOffset)
	if !ok {
		return nil, newParseError(ErrPayloadInvalid)
	}
	size := int(sizeU)
	if size > len(raw) {
		return nil, newParseError(ErrPayloadInvalid)
	}
	var payload []byte
	if size > DataHeaderSize {
		payload = raw[DataHeaderSize:size]
	} else {
		payload = []byte{}
	}
	return DataShred{
		Slot:          slot,
		Index:         index,
		Version:       0,
		FecSetIndex:   index,
		ParentOffset:  0,
		Payload:       payload,
		BatchComplete: flags&dataComplete != 0,
		LastInSlot:    flags&lastInSlot == lastInSlot,
		Variant:       VariantKind{kind: KindDataLegacy},
		RawLen:        len(raw),
	}, nil
}

func ParseShred(raw []byte) (*ParsedShred, bool) {
	k, err := ParseKind(raw)
	if err != nil {
		return nil, false
	}
	d, ok := k.(DataShred)
	if !ok {
		return nil, false
	}
	return &ParsedShred{
		Slot:          d.Slot,
		Index:         d.Index,
		Payload:       d.Payload,
		BatchComplete: d.BatchComplete,
		LastInSlot:    d.LastInSlot,
	}, true
}
