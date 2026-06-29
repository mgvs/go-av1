// Package tile splits a tile group OBU into its individual tiles and starts a
// symbol decoder for each (AV1 spec §5.11.1, tile_group_obu). Each tile is an
// independent entropy-coded unit; decode_tile (partition tree, mode info, residual)
// is built on top in later milestones.
package tile

import (
	"errors"

	"github.com/mgvs/go-av1/bits"
	"github.com/mgvs/go-av1/header"
	"github.com/mgvs/go-av1/msac"
)

// ErrTruncated is returned when the tile group data does not hold the tile sizes it
// claims (a sign of a mis-parsed frame header offset or corrupt stream).
var ErrTruncated = errors.New("tile: truncated tile group")

// Tile is one entropy-coded tile with its mode-info bounds (in 4x4 units).
type Tile struct {
	Row, Col   int
	MiRowStart int
	MiRowEnd   int
	MiColStart int
	MiColEnd   int
	Data       []byte // the tile's entropy-coded bytes
}

// Decoder returns a fresh AV1 symbol decoder over this tile's data, with CDF
// adaptation enabled per the frame's disable_cdf_update flag.
func (t *Tile) Decoder(disableCdfUpdate bool) *msac.Decoder {
	return msac.NewDecoder(t.Data, !disableCdfUpdate)
}

// SplitTileGroup parses a tile group OBU body (the bytes after the frame header in
// a Frame OBU, or a standalone Tile Group OBU payload) and returns its tiles
// (AV1 spec §5.11.1). data must begin at the tile_group_obu start.
func SplitTileGroup(fh *header.FrameHeader, data []byte) ([]Tile, error) {
	numTiles := fh.TileCols * fh.TileRows
	r := bits.NewReader(data)

	tileStartAndEndPresent := false
	if numTiles > 1 {
		tileStartAndEndPresent = r.F(1) == 1
	}
	var tgStart, tgEnd int
	if numTiles == 1 || !tileStartAndEndPresent {
		tgStart = 0
		tgEnd = numTiles - 1
	} else {
		tileBits := fh.TileColsLog2 + fh.TileRowsLog2
		tgStart = int(r.F(tileBits))
		tgEnd = int(r.F(tileBits))
	}
	r.ByteAlign()
	pos := r.Pos() / 8 // byte offset of the first tile size / tile data

	if tgStart > tgEnd || tgStart < 0 || tgEnd >= numTiles {
		return nil, ErrTruncated
	}
	tiles := make([]Tile, 0, tgEnd-tgStart+1)
	for tileNum := tgStart; tileNum <= tgEnd; tileNum++ {
		tileRow := tileNum / fh.TileCols
		tileCol := tileNum % fh.TileCols
		lastTile := tileNum == tgEnd

		var tileSize int
		if lastTile {
			tileSize = len(data) - pos
		} else {
			if pos+fh.TileSizeBytes > len(data) {
				return nil, ErrTruncated
			}
			tileSize = int(readLE(data[pos:pos+fh.TileSizeBytes])) + 1
			pos += fh.TileSizeBytes
		}
		if tileSize <= 0 || pos+tileSize > len(data) {
			return nil, ErrTruncated
		}
		tiles = append(tiles, Tile{
			Row:        tileRow,
			Col:        tileCol,
			MiRowStart: fh.MiRowStarts[tileRow],
			MiRowEnd:   fh.MiRowStarts[tileRow+1],
			MiColStart: fh.MiColStarts[tileCol],
			MiColEnd:   fh.MiColStarts[tileCol+1],
			Data:       data[pos : pos+tileSize],
		})
		pos += tileSize
	}
	return tiles, nil
}

// readLE reads a little-endian unsigned integer from b (descriptor le(n)).
func readLE(b []byte) uint64 {
	var v uint64
	for i := 0; i < len(b); i++ {
		v |= uint64(b[i]) << uint(i*8)
	}
	return v
}
