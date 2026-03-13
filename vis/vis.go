package vis

import "go-quake/bsp"

// DecompressPVS decompresses the RLE-encoded PVS for a leaf's visofs.
// Returns a bitset where bit i = leaf i is visible.
// visofs == -1 means fully visible (returns nil → caller treats as all-visible).
func DecompressPVS(visData []byte, visofs int32, numLeaves int) []byte {
	if visofs < 0 || len(visData) == 0 {
		return nil // all visible
	}

	out := make([]byte, (numLeaves+7)/8)
	src := int(visofs)
	dst := 0

	for dst < len(out) {
		if src >= len(visData) {
			break
		}
		if visData[src] != 0 {
			out[dst] = visData[src]
			src++
			dst++
		} else {
			// RLE: 0x00 followed by skip count (in 8-leaf groups)
			src++
			if src >= len(visData) {
				break
			}
			skip := int(visData[src])
			src++
			dst += skip
		}
	}

	return out
}

// IsLeafVisible returns true if leafIdx is visible from the given PVS bitset.
// nil bitset = all visible.
func IsLeafVisible(pvs []byte, leafIdx int) bool {
	if pvs == nil {
		return true
	}
	// Quake PVS: leaf 0 is the void leaf, PVS starts at leaf 1.
	// leafIdx here is the 1-based BSP leaf number.
	idx := leafIdx - 1
	if idx < 0 {
		return false
	}
	byteIdx := idx / 8
	if byteIdx >= len(pvs) {
		return false
	}
	return pvs[byteIdx]&(1<<uint(idx%8)) != 0
}

// LeafForPoint finds which BSP leaf contains the given point.
func LeafForPoint(m *bsp.Map, point [3]float32) int {
	if len(m.Nodes) == 0 {
		return 0
	}
	node := int32(0)
	for node >= 0 {
		n := &m.Nodes[node]
		p := &m.Planes[n.PlaneNum]
		d := p.Normal[0]*point[0] + p.Normal[1]*point[1] + p.Normal[2]*point[2] - p.Dist
		if d >= 0 {
			node = int32(n.Children[0])
		} else {
			node = int32(n.Children[1])
		}
	}
	// Negative: leaf index = -1 - node
	return int(-1 - node)
}
