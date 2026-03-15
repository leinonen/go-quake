package gfx

import (
	"encoding/binary"
	"fmt"
)

// LMPImage holds decoded pixels for one Quake LMP sprite.
type LMPImage struct {
	Width, Height int
	RGBA          []byte // width*height*4; palette index 255 → alpha=0
}

// DecodeLMP parses raw LMP bytes using the given 768-byte palette.
// Palette index 255 is treated as fully transparent.
func DecodeLMP(data, palette []byte) (*LMPImage, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("lmp too short (%d bytes)", len(data))
	}
	w := int(binary.LittleEndian.Uint32(data[0:4]))
	h := int(binary.LittleEndian.Uint32(data[4:8]))
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("invalid lmp dimensions: %dx%d", w, h)
	}
	pixels := data[8:]
	if len(pixels) < w*h {
		return nil, fmt.Errorf("lmp pixel data too short: need %d, have %d", w*h, len(pixels))
	}
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		idx := pixels[i]
		if idx == 255 {
			// transparent — leave all zero
			continue
		}
		pi := int(idx) * 3
		if pi+2 < len(palette) {
			rgba[i*4+0] = palette[pi+0]
			rgba[i*4+1] = palette[pi+1]
			rgba[i*4+2] = palette[pi+2]
		}
		rgba[i*4+3] = 255
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}, nil
}
