package irml

import "math/bits"

const (
	// DefaultACSubframeGapUs is the inter-subframe negative space threshold
	// used to split multi-section HVAC/AC state frames.
	DefaultACSubframeGapUs int32 = 6000

	maxACDecodeJitter = 0.35
)

// ACFrame represents a parsed multi-subframe HVAC/AC infrared state packet.
type ACFrame struct {
	SubFrames     [][]byte
	SubFrameBits  []int
	ChecksumByte  byte
	ComputedSum8  byte
	ChecksumValid bool
	ShortUs       float64
	LongUs        float64
	TotalBits     int
	Valid         bool
}

// BitDiff highlights a single changed byte between two AC state frames.
type BitDiff struct {
	SubFrameIndex int
	ByteIndex     int
	BitMask       byte
	Before        byte
	After         byte
}

// ACDiffResult holds the bit-by-bit XOR diff between two ACFrame captures.
type ACDiffResult struct {
	Diffs            []BitDiff
	TotalChangedBits int
	LengthMismatch   bool
	ChecksumChanged  bool
}

// ParseACFrame splits a multi-section AC pulse train on inter-frame spaces
// (> 6000 µs), demodulates LSB-first pulse-distance bytes using 1D K-Means
// centroids, and validates an 8-bit additive checksum (SUM8) on the final
// subframe.
func ParseACFrame(pulses []int32) ACFrame {
	var frame ACFrame
	if len(pulses) == 0 {
		return frame
	}
	cleaned, clusters := DenoisePulses(nil, pulses, DefaultGlitchMaxUs)
	if !clusters.Valid || clusters.JitterRatio > maxACDecodeJitter || clusters.LongUs <= clusters.ShortUs {
		return frame
	}
	frame.ShortUs = clusters.ShortUs
	frame.LongUs = clusters.LongUs
	threshold := 0.5 * (clusters.ShortUs + clusters.LongUs)

	start := 0
	for i, p := range cleaned {
		if p < 0 && absInt64(p) > int64(DefaultACSubframeGapUs) {
			appendDecodedSubframe(&frame, cleaned[start:i], threshold)
			start = i + 1
		}
	}
	if start < len(cleaned) {
		appendDecodedSubframe(&frame, cleaned[start:], threshold)
	}
	if len(frame.SubFrames) == 0 {
		return frame
	}
	validateSUM8(&frame)
	frame.Valid = frame.TotalBits > 0
	return frame
}

// DiffACFrames compares two parsed ACFrame captures byte-by-byte and returns
// every changed byte with its XOR bitmask and total flipped bit count.
func DiffACFrames(a, b ACFrame) ACDiffResult {
	var res ACDiffResult
	if len(a.SubFrames) != len(b.SubFrames) {
		res.LengthMismatch = true
	}
	sfCount := min(len(a.SubFrames), len(b.SubFrames))
	for sfIdx := range sfCount {
		sfA := a.SubFrames[sfIdx]
		sfB := b.SubFrames[sfIdx]
		if len(sfA) != len(sfB) {
			res.LengthMismatch = true
		}
		byteCount := min(len(sfA), len(sfB))
		isLastSF := sfIdx == sfCount-1
		for bIdx := range byteCount {
			mask := sfA[bIdx] ^ sfB[bIdx]
			if mask == 0 {
				continue
			}
			res.Diffs = append(res.Diffs, BitDiff{
				SubFrameIndex: sfIdx,
				ByteIndex:     bIdx,
				BitMask:       mask,
				Before:        sfA[bIdx],
				After:         sfB[bIdx],
			})
			res.TotalChangedBits += bits.OnesCount8(mask)
			if isLastSF && bIdx == byteCount-1 {
				res.ChecksumChanged = true
			}
		}
	}
	return res
}

func appendDecodedSubframe(frame *ACFrame, seg []int32, threshold float64) {
	if len(seg) < 4 {
		return
	}
	i := skipHeaderPair(seg)
	var data []byte
	bitCount := 0
	for i+1 < len(seg) {
		mark := seg[i]
		space := seg[i+1]
		if mark <= 0 || space >= 0 {
			i++
			continue
		}
		if absInt64(mark) >= int64(DefaultHeaderMinUs) {
			i += 2
			continue
		}
		if bitCount%8 == 0 {
			data = append(data, 0)
		}
		if float64(absInt64(space)) > threshold {
			data[bitCount/8] |= 1 << (bitCount % 8)
		}
		bitCount++
		i += 2
	}
	if bitCount == 0 {
		return
	}
	frame.SubFrames = append(frame.SubFrames, data)
	frame.SubFrameBits = append(frame.SubFrameBits, bitCount)
	frame.TotalBits += bitCount
}

func skipHeaderPair(seg []int32) int {
	if len(seg) >= 2 && seg[0] > 0 && absInt64(seg[0]) >= int64(DefaultHeaderMinUs) {
		if seg[1] < 0 {
			return 2
		}
		return 1
	}
	return 0
}

func validateSUM8(frame *ACFrame) {
	last := frame.SubFrames[len(frame.SubFrames)-1]
	if len(last) < 2 {
		return
	}
	var sum byte
	for _, b := range last[:len(last)-1] {
		sum += b
	}
	frame.ComputedSum8 = sum
	frame.ChecksumByte = last[len(last)-1]
	frame.ChecksumValid = sum == frame.ChecksumByte
}
