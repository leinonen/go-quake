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

// weaponColors defines the background colour for each weapon slot's fallback icon.
var weaponColors = [8][3]byte{
	{50, 50, 50},    // 0 axe: dark grey
	{60, 45, 25},    // 1 shotgun: dark tan
	{60, 50, 25},    // 2 super shotgun: slightly brighter tan
	{20, 40, 70},    // 3 nailgun: dark blue
	{25, 50, 80},    // 4 super nailgun: medium blue
	{40, 40, 10},    // 5 grenade launcher: dark olive
	{70, 30, 10},    // 6 rocket launcher: dark orange
	{70, 60, 10},    // 7 lightning gun: dark yellow
}

// weaponLabels are the 2-character abbreviations shown in fallback weapon icons.
var weaponLabels = [8]string{"AX", "SG", "SS", "NG", "SN", "GL", "RL", "LG"}

// letterFont is a 5×7 bitmap font for uppercase letters needed in weapon labels.
var letterFont = map[byte][7]uint8{
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'G': {0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01110},
	'L': {0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111},
	'N': {0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'X': {0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001},
}

// GenerateWeaponIcon returns a 32×24 RGBA icon for weapon slot s showing a
// 2-character abbreviation over a coloured background.
// bright=true: full colour (owned); bright=false: dimmed (unowned).
func GenerateWeaponIcon(slot int, bright bool) *LMPImage {
	const w, h = 32, 24
	rgba := make([]byte, w*h*4)
	if slot < 0 || slot >= len(weaponColors) {
		return &LMPImage{Width: w, Height: h, RGBA: rgba}
	}
	bg := weaponColors[slot]
	bgR, bgG, bgB := bg[0], bg[1], bg[2]
	if bright {
		// Brighter background for owned weapons.
		bgR = bgR*2 + 20
		bgG = bgG*2 + 20
		bgB = bgB*2 + 20
	}
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = bgR
		rgba[i*4+1] = bgG
		rgba[i*4+2] = bgB
		rgba[i*4+3] = 255
	}

	// Render the 2-letter label at scale 2 (each pixel → 2×2 block), centred.
	// A glyph is 5×7 at scale 2 = 10×14 px. Two glyphs + 2px gap = 22px wide, 14px tall.
	const scale = 2
	const glyphW = 5 * scale // 10
	const glyphH = 7 * scale // 14
	const gap = 2
	label := weaponLabels[slot]
	totalW := len(label)*glyphW + (len(label)-1)*gap
	startX := (w - totalW) / 2
	startY := 2 // top-aligned; bottom 8px reserved for ammo digit overlay

	textR, textG, textB := byte(255), byte(255), byte(255)
	if !bright {
		textR, textG, textB = 140, 140, 140
	}

	for ci, ch := range label {
		glyph, ok := letterFont[byte(ch)]
		if !ok {
			continue
		}
		baseX := startX + ci*(glyphW+gap)
		for row := 0; row < 7; row++ {
			for col := 0; col < 5; col++ {
				if glyph[row]&(1<<(4-col)) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						px := baseX + col*scale + dx
						py := startY + row*scale + dy
						if px >= 0 && px < w && py >= 0 && py < h {
							i := (py*w + px) * 4
							rgba[i+0] = textR
							rgba[i+1] = textG
							rgba[i+2] = textB
							rgba[i+3] = 255
						}
					}
				}
			}
		}
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}
}

// GenerateSmallDigit returns an 8×8 RGBA pixel-art digit using digitFont at scale=1.
func GenerateSmallDigit(n int) *LMPImage {
	const w, h = 8, 8
	rgba := make([]byte, w*h*4)
	if n < 0 || n > 9 {
		return &LMPImage{Width: w, Height: h, RGBA: rgba}
	}
	// digitFont is 5×7; place at offset (1,0) within 8×8.
	font := digitFont[n]
	for row := 0; row < 7; row++ {
		for col := 0; col < 5; col++ {
			if font[row]&(1<<(4-col)) == 0 {
				continue
			}
			px := 1 + col
			py := row
			if px < w && py < h {
				i := (py*w + px) * 4
				rgba[i+0] = 255
				rgba[i+1] = 220
				rgba[i+2] = 0
				rgba[i+3] = 255
			}
		}
	}
	return &LMPImage{Width: w, Height: h, RGBA: rgba}
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
