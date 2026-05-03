package shredstream

const (
	ProofNodeSize = 20
	ResignedExtra = 64
)

const (
	KindDataLegacy = iota
	KindCodeLegacy
	KindDataMerkle
	KindCodeMerkle
)

type VariantKind struct {
	kind      int
	proofSize uint8
	resigned  bool
}

func (v VariantKind) Kind() int { return v.kind }

func (v VariantKind) IsData() bool {
	return v.kind == KindDataLegacy || v.kind == KindDataMerkle
}

func (v VariantKind) IsCode() bool {
	return v.kind == KindCodeLegacy || v.kind == KindCodeMerkle
}

func (v VariantKind) MerkleSuffix() int {
	switch v.kind {
	case KindDataLegacy, KindCodeLegacy:
		return 0
	case KindDataMerkle, KindCodeMerkle:
		proof := int(v.proofSize) * ProofNodeSize
		merkleRoot := 32
		resignedSig := 0
		if v.resigned {
			resignedSig = ResignedExtra
		}
		return proof + merkleRoot + resignedSig
	}
	return 0
}

func (v VariantKind) ProofSize() uint8 {
	switch v.kind {
	case KindDataMerkle, KindCodeMerkle:
		return v.proofSize
	}
	return 0
}

func (v VariantKind) Resigned() bool {
	switch v.kind {
	case KindDataMerkle, KindCodeMerkle:
		return v.resigned
	}
	return false
}

func (a VariantKind) Equal(b VariantKind) bool {
	return a.kind == b.kind && a.proofSize == b.proofSize && a.resigned == b.resigned
}

func ClassifyVariant(b byte) (VariantKind, bool) {
	switch {
	case b == 0xA5:
		return VariantKind{kind: KindDataLegacy}, true
	case b == 0x5A:
		return VariantKind{kind: KindCodeLegacy}, true
	case b >= 0x60 && b <= 0x6F:
		return VariantKind{kind: KindCodeMerkle, proofSize: b & 0x0F, resigned: false}, true
	case b >= 0x70 && b <= 0x7F:
		return VariantKind{kind: KindCodeMerkle, proofSize: b & 0x0F, resigned: true}, true
	case b >= 0x90 && b <= 0x9F:
		return VariantKind{kind: KindDataMerkle, proofSize: b & 0x0F, resigned: false}, true
	case b >= 0xB0 && b <= 0xBF:
		return VariantKind{kind: KindDataMerkle, proofSize: b & 0x0F, resigned: true}, true
	}
	return VariantKind{}, false
}
