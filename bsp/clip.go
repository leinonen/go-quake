package bsp

// TraceResult is the result of a hull trace.
type TraceResult struct {
	Fraction   float32    // 0..1, fraction of movement completed before impact
	Normal     [3]float32 // surface normal at impact
	EndPos     [3]float32 // final position
	Hit        bool       // true if movement was blocked
	StartSolid bool       // true if the trace started inside solid
	AllSolid   bool       // true if the entire trace was inside solid
}

const hullEps = 0.03125 // 1/32, same as Quake

// HullPointContents returns the leaf contents at point within the hull rooted at num.
func HullPointContents(clipNodes []DClipNode, planes []DPlane, num int32, point [3]float32) int {
	for num >= 0 {
		node := &clipNodes[num]
		plane := &planes[node.PlaneNum]
		var d float32
		if plane.Type < 3 {
			d = point[plane.Type] - plane.Dist
		} else {
			d = plane.Normal[0]*point[0] + plane.Normal[1]*point[1] + plane.Normal[2]*point[2] - plane.Dist
		}
		if d > 0 {
			num = int32(node.Children[0])
		} else {
			num = int32(node.Children[1])
		}
	}
	return int(num)
}

// HullTrace traces the segment p1→p2 through the hull and returns the first solid impact.
func HullTrace(clipNodes []DClipNode, planes []DPlane, headNode int32, p1, p2 [3]float32) TraceResult {
	tr := TraceResult{
		Fraction: 1.0,
		EndPos:   p2,
		AllSolid: true,
	}
	recursiveHullCheck(clipNodes, planes, headNode, 0, 1, p1, p2, &tr)
	if tr.Fraction < 1.0 {
		for i := 0; i < 3; i++ {
			tr.EndPos[i] = p1[i] + tr.Fraction*(p2[i]-p1[i])
		}
		tr.Hit = true
	}
	return tr
}

// recursiveHullCheck implements Quake's SV_RecursiveHullCheck.
// Returns true if no solid impact was found in the interval [p1f, p2f].
func recursiveHullCheck(clipNodes []DClipNode, planes []DPlane, num int32, p1f, p2f float32, p1, p2 [3]float32, tr *TraceResult) bool {
	if num < 0 {
		if int(num) != ContentsSolid {
			tr.AllSolid = false
		} else {
			tr.StartSolid = true
		}
		return true
	}

	node := &clipNodes[num]
	plane := &planes[node.PlaneNum]

	var t1, t2 float32
	if plane.Type < 3 {
		t1 = p1[plane.Type] - plane.Dist
		t2 = p2[plane.Type] - plane.Dist
	} else {
		t1 = plane.Normal[0]*p1[0] + plane.Normal[1]*p1[1] + plane.Normal[2]*p1[2] - plane.Dist
		t2 = plane.Normal[0]*p2[0] + plane.Normal[1]*p2[1] + plane.Normal[2]*p2[2] - plane.Dist
	}

	// Both points on the same side — recurse into that side only.
	if t1 >= 0 && t2 >= 0 {
		return recursiveHullCheck(clipNodes, planes, int32(node.Children[0]), p1f, p2f, p1, p2, tr)
	}
	if t1 < 0 && t2 < 0 {
		return recursiveHullCheck(clipNodes, planes, int32(node.Children[1]), p1f, p2f, p1, p2, tr)
	}

	// Segment crosses the plane — split and recurse near side first.
	side := 0
	var frac float32
	if t1 < 0 {
		side = 1
		frac = (t1 + hullEps) / (t1 - t2)
	} else {
		frac = (t1 - hullEps) / (t1 - t2)
	}
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}

	midf := p1f + (p2f-p1f)*frac
	mid := [3]float32{
		p1[0] + frac*(p2[0]-p1[0]),
		p1[1] + frac*(p2[1]-p1[1]),
		p1[2] + frac*(p2[2]-p1[2]),
	}

	// Recurse into near side.
	if !recursiveHullCheck(clipNodes, planes, int32(node.Children[side]), p1f, midf, p1, mid, tr) {
		return false
	}

	// If far side is solid, this is the impact point.
	if HullPointContents(clipNodes, planes, int32(node.Children[side^1]), mid) == ContentsSolid {
		if tr.AllSolid {
			return false
		}
		if side == 0 {
			tr.Normal = plane.Normal
		} else {
			tr.Normal = [3]float32{-plane.Normal[0], -plane.Normal[1], -plane.Normal[2]}
		}
		tr.Fraction = midf
		tr.EndPos = mid
		return false
	}

	// Recurse into far side.
	return recursiveHullCheck(clipNodes, planes, int32(node.Children[side^1]), midf, p2f, mid, p2, tr)
}
