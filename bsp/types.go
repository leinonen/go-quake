package bsp

// BSP29 lump indices
const (
	LumpEntities     = 0
	LumpPlanes       = 1
	LumpTextures     = 2
	LumpVertices     = 3
	LumpVisibility   = 4
	LumpNodes        = 5
	LumpTexInfo      = 6
	LumpFaces        = 7
	LumpLighting     = 8
	LumpClipNodes    = 9
	LumpLeaves       = 10
	LumpMarkSurfaces = 11
	LumpEdges        = 12
	LumpSurfEdges    = 13
	LumpModels       = 14
	NumLumps         = 15
)

const BSPVersion = 29

// DLump is a lump directory entry.
type DLump struct {
	Offset int32
	Length int32
}

// DHeader is the BSP file header.
type DHeader struct {
	Version int32
	Lumps   [NumLumps]DLump
}

// DPlane is an on-disk BSP plane.
type DPlane struct {
	Normal [3]float32
	Dist   float32
	Type   int32
}

// DNode is an internal BSP node.
type DNode struct {
	PlaneNum  int32
	Children  [2]int16 // negative = leaf index (-1 - leafnum)
	Mins      [3]int16
	Maxs      [3]int16
	FirstFace uint16
	NumFaces  uint16
}

// DLeaf is a BSP leaf (convex volume).
type DLeaf struct {
	Contents        int32
	VisOfs          int32  // -1 = no vis data
	Mins            [3]int16
	Maxs            [3]int16
	FirstMarkSurface uint16
	NumMarkSurfaces  uint16
	AmbientLevel    [4]uint8
}

// DFace is an on-disk BSP face.
type DFace struct {
	PlaneNum  uint16
	Side      int16
	FirstEdge int32
	NumEdges  int16
	TexInfo   int16
	Styles    [4]uint8
	LightOfs  int32
}

// DEdge is an on-disk BSP edge.
type DEdge struct {
	V [2]uint16
}

// DTexInfo is an on-disk texture info record (lump 6, 40 bytes).
type DTexInfo struct {
	Vecs   [2][4]float32 // s and t axis vectors + offsets (32 bytes)
	MipTex int32
	Flags  int32
}

// MipTex holds a decoded miptex entry (lump 2).
type MipTex struct {
	Name   string
	Width  int
	Height int
	Pixels []byte // mip0: Width*Height palette indices; nil if not present
}

// DClipNode is a collision BSP node (lump 9, 8 bytes).
type DClipNode struct {
	PlaneNum int32
	Children [2]int16 // positive = clip node index, negative = leaf contents
}

// Leaf contents values returned from hull traversal.
const (
	ContentsEmpty = -1
	ContentsSolid = -2
	ContentsWater = -3
	ContentsSlime = -4
	ContentsLava  = -5
	ContentsSky   = -6
)

// DModel is a BSP model (world + brush entities).
type DModel struct {
	Mins      [3]float32
	Maxs      [3]float32
	Origin    [3]float32
	HeadNodes [4]int32
	VisLeafs  int32
	FirstFace int32
	NumFaces  int32
}
