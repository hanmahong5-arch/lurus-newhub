package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

// nilfixBuildOggPage builds one minimal OggS page carrying the given granule
// position, so the tests below can drive getOpusDuration deterministically
// without shipping a binary fixture.
func nilfixBuildOggPage(granulePos uint64, dataLen int) []byte {
	buf := &bytes.Buffer{}
	buf.WriteString("OggS")
	buf.WriteByte(0)                                       // version
	buf.WriteByte(0)                                       // header type
	_ = binary.Write(buf, binary.LittleEndian, granulePos) // granule position
	_ = binary.Write(buf, binary.LittleEndian, uint32(1))  // serial
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))  // page sequence
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))  // checksum
	buf.WriteByte(1)                                       // number of segments
	buf.WriteByte(byte(dataLen))                           // segment table: one segment
	buf.Write(make([]byte, dataLen))                       // page payload
	return buf.Bytes()
}

// TestNilfixOpusUndecodableStreamIsNotBillableAsZero pins the billing contract
// for the ogg/opus branch: an undecodable body must surface an error, never
// (0, nil).
//
// Why it matters: the TTS settlement path only reaches its size-based fallback
// estimate when GetAudioDuration returns an error
// (internal/adapter/provider/openai/audio.go: `if durationErr != nil { ... }
// else if duration > 0 { ... }`). A (0, nil) return matches neither arm, so the
// response is billed zero completion tokens — the "silently-free response" the
// sibling TTS tests already guard against for other formats. The STT
// pre-consume estimator (internal/app/token_counter.go EstimateRequestToken)
// likewise turns (0, nil) into a 0-token estimate.
func TestNilfixOpusUndecodableStreamIsNotBillableAsZero(t *testing.T) {
	// 64 bytes that are not an OGG container at all — same corpus shape the
	// existing invalid-input test uses for wav/flac/aiff/aac.
	garbage := bytes.Repeat([]byte{0x00, 0x11, 0x22, 0x33}, 16)

	for _, ext := range []string{".ogg", ".oga", ".opus"} {
		d, err := GetAudioDuration(context.Background(), bytes.NewReader(garbage), ext)
		if err == nil {
			t.Errorf("%s with garbage input returned (%v, nil); an undecodable audio body must error so callers can fall back instead of billing zero", ext, d)
		}
	}
}

// TestNilfixOpusHeaderOnlyStreamErrors covers the truncated-stream shape: a
// well-formed OggS page whose granule position is 0 (an OpusHead/OpusTags-only
// stream carries no audio). Reporting 0 seconds as success would bill nothing.
func TestNilfixOpusHeaderOnlyStreamErrors(t *testing.T) {
	headerOnly := nilfixBuildOggPage(0, 8)

	d, err := GetAudioDuration(context.Background(), bytes.NewReader(headerOnly), ".opus")
	if err == nil {
		t.Errorf("header-only opus stream returned (%v, nil); want an error so the caller uses its fallback estimate", d)
	}
}

// TestNilfixOpusValidStreamStillParses is the over-correction guard: a real
// stream with a positive granule position must keep parsing successfully.
func TestNilfixOpusValidStreamStillParses(t *testing.T) {
	page := nilfixBuildOggPage(96000, 12) // 96000 samples @ fixed 48kHz = 2s

	d, err := GetAudioDuration(context.Background(), bytes.NewReader(page), ".opus")
	if err != nil {
		t.Fatalf("valid opus page should still parse, got error: %v", err)
	}
	if d < 1.99 || d > 2.01 {
		t.Errorf("duration = %v, want ~2.0s (granule 96000 / 48000 Hz)", d)
	}
}
