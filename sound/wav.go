package sound

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"bytes"
)

// wavData holds the decoded PCM audio data and format information.
type wavData struct {
	format     uint32 // AL format constant
	data       []byte
	sampleRate uint32
}

// decodeWAV parses a RIFF/WAV file and returns the PCM data with format info.
// Supports 8-bit and 16-bit mono/stereo PCM (formats Quake uses).
func decodeWAV(b []byte) (*wavData, error) {
	r := bytes.NewReader(b)

	// RIFF header
	var riffID [4]byte
	if _, err := io.ReadFull(r, riffID[:]); err != nil {
		return nil, fmt.Errorf("wav: read RIFF: %w", err)
	}
	if string(riffID[:]) != "RIFF" {
		return nil, errors.New("wav: not a RIFF file")
	}

	var chunkSize uint32
	if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
		return nil, fmt.Errorf("wav: read chunk size: %w", err)
	}

	var waveID [4]byte
	if _, err := io.ReadFull(r, waveID[:]); err != nil {
		return nil, fmt.Errorf("wav: read WAVE: %w", err)
	}
	if string(waveID[:]) != "WAVE" {
		return nil, errors.New("wav: not a WAVE file")
	}

	var fmtChannels uint16
	var fmtSampleRate uint32
	var fmtBitsPerSample uint16
	var pcmData []byte

	// Parse chunks
	for {
		var chunkID [4]byte
		if _, err := io.ReadFull(r, chunkID[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, fmt.Errorf("wav: read chunk id: %w", err)
		}
		var sz uint32
		if err := binary.Read(r, binary.LittleEndian, &sz); err != nil {
			return nil, fmt.Errorf("wav: read chunk size: %w", err)
		}

		switch string(chunkID[:]) {
		case "fmt ":
			var audioFmt uint16
			if err := binary.Read(r, binary.LittleEndian, &audioFmt); err != nil {
				return nil, fmt.Errorf("wav: fmt audioFormat: %w", err)
			}
			if audioFmt != 1 {
				return nil, fmt.Errorf("wav: unsupported audio format %d (only PCM=1)", audioFmt)
			}
			if err := binary.Read(r, binary.LittleEndian, &fmtChannels); err != nil {
				return nil, fmt.Errorf("wav: fmt channels: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &fmtSampleRate); err != nil {
				return nil, fmt.Errorf("wav: fmt sampleRate: %w", err)
			}
			// Skip byteRate and blockAlign
			var skip [6]byte
			if _, err := io.ReadFull(r, skip[:]); err != nil {
				return nil, fmt.Errorf("wav: fmt skip: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &fmtBitsPerSample); err != nil {
				return nil, fmt.Errorf("wav: fmt bitsPerSample: %w", err)
			}
			// Skip any remaining fmt chunk bytes (e.g. extended fmt)
			remaining := int(sz) - 16
			if remaining > 0 {
				extra := make([]byte, remaining)
				if _, err := io.ReadFull(r, extra); err != nil {
					return nil, fmt.Errorf("wav: fmt extra: %w", err)
				}
			}
		case "data":
			pcmData = make([]byte, sz)
			if _, err := io.ReadFull(r, pcmData); err != nil {
				return nil, fmt.Errorf("wav: data: %w", err)
			}
		default:
			// Skip unknown chunks (including LIST, fact, etc.)
			skip := make([]byte, sz)
			if _, err := io.ReadFull(r, skip); err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					break
				}
				return nil, fmt.Errorf("wav: skip chunk %s: %w", chunkID, err)
			}
		}
		// Chunks are word-aligned; skip padding byte if needed
		if sz%2 != 0 {
			var pad [1]byte
			_, _ = io.ReadFull(r, pad[:])
		}
	}

	if pcmData == nil {
		return nil, errors.New("wav: no data chunk found")
	}
	if fmtChannels == 0 || fmtSampleRate == 0 || fmtBitsPerSample == 0 {
		return nil, errors.New("wav: missing fmt chunk")
	}

	// Map to OpenAL format constant values (defined in al.h)
	// AL_FORMAT_MONO8=0x1100, AL_FORMAT_MONO16=0x1101, AL_FORMAT_STEREO8=0x1102, AL_FORMAT_STEREO16=0x1103
	var alFmt uint32
	switch {
	case fmtChannels == 1 && fmtBitsPerSample == 8:
		alFmt = 0x1100
	case fmtChannels == 1 && fmtBitsPerSample == 16:
		alFmt = 0x1101
	case fmtChannels == 2 && fmtBitsPerSample == 8:
		alFmt = 0x1102
	case fmtChannels == 2 && fmtBitsPerSample == 16:
		alFmt = 0x1103
	default:
		return nil, fmt.Errorf("wav: unsupported format: %d channels, %d bits", fmtChannels, fmtBitsPerSample)
	}

	return &wavData{
		format:     alFmt,
		data:       pcmData,
		sampleRate: fmtSampleRate,
	}, nil
}
