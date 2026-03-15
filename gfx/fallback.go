package gfx

// GenerateHUDSprites returns synthetically generated LMP-equivalent sprites
// for the status bar when the original LMP files are absent from the PAK.
//
// Sizes match the original Quake layout:
//   - SBar: 320×24 dark background
//   - Digits: 24×24 each (0–9), pixel-art font on transparent background
//   - Faces: 24×24 each (5 health ranges), solid colour squares

func GenerateSBar() *LMPImage {
	const w, h = 320, 24
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 20
		rgba[i*4+1] = 20
		rgba[i*4+2] = 20
		rgba[i*4+3] = 210
	}
	// subtle lighter border at top
	for x := 0; x < w; x++ {
		rgba[(0*w+x)*4+0] = 60
		rgba[(0*w+x)*4+1] = 60
		rgba[(0*w+x)*4+2] = 60
		rgba[(0*w+x)*4+3] = 230
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}
}

// digitFont is a 5-wide × 7-tall bitmap font for digits 0–9.
// Each row is a 5-bit mask (MSB = leftmost pixel).
var digitFont = [10][7]uint8{
	{0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110}, // 0
	{0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110}, // 1
	{0b01110, 0b10001, 0b00001, 0b00110, 0b01000, 0b10000, 0b11111}, // 2
	{0b11110, 0b00001, 0b00001, 0b01110, 0b00001, 0b00001, 0b11110}, // 3
	{0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010}, // 4
	{0b11111, 0b10000, 0b10000, 0b11110, 0b00001, 0b00001, 0b11110}, // 5
	{0b01110, 0b10000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110}, // 6
	{0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000}, // 7
	{0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110}, // 8
	{0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00001, 0b01110}, // 9
}

// GenerateDigit returns a 24×24 RGBA image with a pixel-art digit rendered in
// bright yellow on a transparent background.
func GenerateDigit(n int) *LMPImage {
	const w, h = 24, 24
	rgba := make([]byte, w*h*4) // all transparent

	if n < 0 || n > 9 {
		return &LMPImage{Width: w, Height: h, RGBA: rgba}
	}

	// Font is 5 wide × 7 tall; scale each pixel to 3×3.
	// Total: 15×21 — centre in 24×24: left offset = 4, top offset = 1.
	const scale = 3
	const offX, offY = 4, 1

	font := digitFont[n]
	for row := 0; row < 7; row++ {
		for col := 0; col < 5; col++ {
			if font[row]&(1<<(4-col)) == 0 {
				continue
			}
			// Fill a scale×scale block.
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					px := offX + col*scale + dx
					py := offY + row*scale + dy
					if px >= 0 && px < w && py >= 0 && py < h {
						i := (py*w + px) * 4
						rgba[i+0] = 255 // R
						rgba[i+1] = 220 // G  → bright yellow
						rgba[i+2] = 0   // B
						rgba[i+3] = 255
					}
				}
			}
		}
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}
}

// faceColors maps health range index (0=high … 4=low) to a solid RGB colour
// for the placeholder face sprite.
var faceColors = [5][3]byte{
	{80, 200, 80},  // 0: healthy green
	{160, 200, 60}, // 1: yellow-green
	{220, 180, 40}, // 2: yellow
	{220, 100, 30}, // 3: orange
	{200, 40, 40},  // 4: red (low health)
}

// GenerateFace returns a 24×24 RGBA image with a simple solid-colour square
// indicating the current health range.
func GenerateFace(idx int) *LMPImage {
	const w, h = 24, 24
	rgba := make([]byte, w*h*4)

	if idx < 0 || idx > 4 {
		idx = 0
	}
	col := faceColors[idx]
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = col[0]
		rgba[i*4+1] = col[1]
		rgba[i*4+2] = col[2]
		rgba[i*4+3] = 220
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}
}
