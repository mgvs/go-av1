// Package predict holds AV1 intra prediction and the block-geometry constant
// tables shared by partition/mode-info decoding and reconstruction (AV1 spec §6.10,
// §7.11.2). It is independent of the entropy decoder so it can be unit-tested in
// isolation. Only DC prediction is implemented so far (milestone M4); the other
// predictors (Paeth/Smooth/directional/CfL/filter-intra) follow.
package predict

// Block sizes (AV1 spec §6.10.4, BLOCK_*). Index into the geometry tables below.
const (
	Block4x4 = iota
	Block4x8
	Block8x4
	Block8x8
	Block8x16
	Block16x8
	Block16x16
	Block16x32
	Block32x16
	Block32x32
	Block32x64
	Block64x32
	Block64x64
	Block64x128
	Block128x64
	Block128x128
	Block4x16
	Block16x4
	Block8x32
	Block32x8
	Block16x64
	Block64x16
	BlockSizes
	BlockInvalid = -1
)

// Partition types (AV1 spec §6.10.4, PARTITION_*).
const (
	PartitionNone = iota
	PartitionHorz
	PartitionVert
	PartitionSplit
	PartitionHorzA
	PartitionHorzB
	PartitionVertA
	PartitionVertB
	PartitionHorz4
	PartitionVert4
)

// Geometry tables, indexed by block size (AV1 spec §9.3 / additional tables).
var (
	Num4x4BlocksWide = [BlockSizes]int{1, 1, 2, 2, 2, 4, 4, 4, 8, 8, 8, 16, 16, 16, 32, 32, 1, 4, 2, 8, 4, 16}
	Num4x4BlocksHigh = [BlockSizes]int{1, 2, 1, 2, 4, 2, 4, 8, 4, 8, 16, 8, 16, 32, 16, 32, 4, 1, 8, 2, 16, 4}
	MiWidthLog2      = [BlockSizes]int{0, 0, 1, 1, 1, 2, 2, 2, 3, 3, 3, 4, 4, 4, 5, 5, 0, 2, 1, 3, 2, 4}
	MiHeightLog2     = [BlockSizes]int{0, 1, 0, 1, 2, 1, 2, 3, 2, 3, 4, 3, 4, 5, 4, 5, 2, 0, 3, 1, 4, 2}
	SizeGroup        = [BlockSizes]int{0, 0, 0, 1, 1, 1, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 0, 0, 1, 1, 2, 2}
)

// BlockWidth returns the block width in luma samples.
func BlockWidth(bSize int) int { return 4 * Num4x4BlocksWide[bSize] }

// BlockHeight returns the block height in luma samples.
func BlockHeight(bSize int) int { return 4 * Num4x4BlocksHigh[bSize] }

// PartitionSubsize[p][bSize] is the sub-block size produced by partition p of a
// square block bSize (AV1 spec §9.3). Only square columns are valid.
var PartitionSubsize = [10][BlockSizes]int{
	{ // PARTITION_NONE
		Block4x4, -1, -1, Block8x8, -1, -1, Block16x16, -1, -1, Block32x32, -1, -1, Block64x64, -1, -1, Block128x128, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_HORZ
		-1, -1, -1, Block8x4, -1, -1, Block16x8, -1, -1, Block32x16, -1, -1, Block64x32, -1, -1, Block128x64, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_VERT
		-1, -1, -1, Block4x8, -1, -1, Block8x16, -1, -1, Block16x32, -1, -1, Block32x64, -1, -1, Block64x128, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_SPLIT
		-1, -1, -1, Block4x4, -1, -1, Block8x8, -1, -1, Block16x16, -1, -1, Block32x32, -1, -1, Block64x64, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_HORZ_A
		-1, -1, -1, Block8x4, -1, -1, Block16x8, -1, -1, Block32x16, -1, -1, Block64x32, -1, -1, Block128x64, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_HORZ_B
		-1, -1, -1, Block8x4, -1, -1, Block16x8, -1, -1, Block32x16, -1, -1, Block64x32, -1, -1, Block128x64, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_VERT_A
		-1, -1, -1, Block4x8, -1, -1, Block8x16, -1, -1, Block16x32, -1, -1, Block32x64, -1, -1, Block64x128, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_VERT_B
		-1, -1, -1, Block4x8, -1, -1, Block8x16, -1, -1, Block16x32, -1, -1, Block32x64, -1, -1, Block64x128, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_HORZ_4
		-1, -1, -1, -1, -1, -1, Block16x4, -1, -1, Block32x8, -1, -1, Block64x16, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	},
	{ // PARTITION_VERT_4
		-1, -1, -1, -1, -1, -1, Block4x16, -1, -1, Block8x32, -1, -1, Block16x64, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	},
}
