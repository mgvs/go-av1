# go-av1

Pure-Go AV1 video decoder (no cgo). Original implementation from the public
**AV1 Bitstream & Decoding Process Specification** (Alliance for Open Media),
cross-checked against the BSD-2 references `dav1d` and `libaom`. AV1 is open and
royalty-free (AOMedia, patent-free by design).

> **Status: milestone-based, M1–M8 complete.** A from-scratch full AV1 decoder, built
> and bit-exactly verified one milestone at a time against dav1d: intra + inter
> prediction, all in-loop filters, reference/superres scaling, show_existing_frame,
> film grain, high bit depth (10/12-bit) and every chroma format (4:2:0/4:2:2/4:4:4/mono).

## Milestone roadmap

| # | Milestone | State |
|---|-----------|-------|
| M0 | OBU framing: LEB128, OBU header/extension, temporal-unit split | ✅ |
| M1 | Symbol decoder (msac: CDF range coding + adaptation) + matching encoder, roundtrip-tested | ✅ |
| M2 | Sequence header OBU; frame header OBU (frame size, refs, tiles, quant, segmentation) | ✅ (intra + inter) |
| M3 | Tile decode; partition tree; mode info (intra) | ✅ tile split, partition recursion, intra mode info |
| M4 | Intra prediction (DC/Paeth/Smooth/directional + CfL), keyframe reconstruction | ✅ all intra predictors (DC/Paeth/Smooth*/directional+angle+edge-filter/filter-intra/CfL) + tx_mode_select + extended partitions; a full 64×64 grayscale keyframe decodes bit-exact |
| M5 | Transforms (DCT/ADST/FLIPADST/IDTX, 4..64) + dequant; coefficient decode | ✅ full coeff decode (eob/scan/coeff_base+br/sign/golomb, neighbor contexts) + dequant + inverse DCT/ADST/FLIPADST/IDTX at all tx sizes + tx_type signaling, cross-checked bit-exactly against libaom |

> **Matching frames ✅** — bit-exact with dav1d, entropy symbols cross-checked
> against the libaom inspect oracle:
> - **flat** 64×64 keyframe (DC prediction, all-zero residual) — `decode/decode_test.go`.
> - **textured DC** 64×64 keyframe (one coded DC coefficient) — `decode/coeff_test.go`.
> - **gradient** 64×64 keyframe (46 AC coefficients → multi-coefficient inverse
>   64×64 DCT) — `decode/grad_test.go`.
> - **dense** 64×64 keyframe (radial cosine, 231 AC coefficients, CDEF disabled) —
>   `decode/dense_test.go`. Stresses the coeff_base scan loop and 2D contexts.
> - **multi-block** 128×64 keyframe (two 64×64 blocks, in-loop filters disabled) —
>   `decode/multiblock_test.go`. Exercises the superblock-boundary partition, DC
>   prediction of the 2nd block from the 1st's reconstructed edge, the cross-block
>   coefficient level/DC contexts and a coded chroma TX_32X32 residual.
> - **smooth-pred** 16×16 keyframe — `decode/smooth_test.go`. A SMOOTH_V-predicted
>   block with a signaled luma intra_tx_type and the TX_16X16 coefficient path.
> - **directional** 32×32 grayscale keyframe — `decode/directional_test.go`. A rich
>   mix of DC, directional (D45/D135/D157/D203), SMOOTH, SMOOTH_H and Paeth modes
>   with tx_mode_select and split transform sizes.
> - **gray** 64×64 grayscale keyframe — `decode/gray_test.go`. The full intra set:
>   DC, all directional angles (+angle delta, intra edge filter), SMOOTH/V/H, Paeth,
>   recursive filter-intra, chroma-from-luma (CfL), extended partitions and
>   tx_mode_select. A complete natural keyframe, byte-exact with dav1d.
> - **deblock / CDEF** keyframes — `decode/loopfilter_test.go`, `decode/cdef_test.go`.
>   The deblocking loop filter (level 20) and CDEF (primary + secondary strengths).
> - **loop restoration** 256×256 keyframe — `decode/lr_test.go`. All three in-loop
>   filters together: deblocking + CDEF + Wiener restoration (read_lr subexp-coded
>   taps, the separable Wiener filter, the stripe source selection).
> - **self-guided restoration** 128×128 keyframe — `decode/sgr_test.go`. SGR loop
>   restoration: the box filter (A/B integral arrays + per-pass weights) and the
>   projection onto the CDEF output.
> - **inter keyframe pair** 64×64 — `decode/inter_test.go`. First inter decode:
>   reference store + inter mode info + find_mv_stack + a zero-MV NEARESTMV skip
>   block whose motion compensation is an exact copy of the reference.
> - **variable-transform inter** 64×64 — `decode/vartx_test.go`. NEARESTMV + NEWMV
>   32×32 inter blocks with coded residual, the variable transform tree
>   (read_var_tx_size / transform_tree), inter transform-type sets, read_mv
>   (sub-pel MVs) and 8-tap sub-pel motion compensation.
> - **motion-mode inter** 64×64 — `decode/motionmode_test.go`. The same with
>   is_motion_mode_switchable enabled, so each block reads the use_obmc symbol
>   (resolving to SIMPLE) to stay entropy-synced.
> - **compound / wedge** 12-frame 64×64 stream — `decode/wedge_test.go` (`cmp4`).
>   Masked-compound prediction (WEDGE + DIFFWTD masks), the compound extra-MV-search
>   combination (spec §7.10.2.12) and multi-reference CDF load/save with the
>   frame-context symbol-counter reset. Every one of the 12 frames is byte-exact.
> - **OBMC** 6-frame 64×64 stream — `decode/obmc_test.go`. Overlapped block motion
>   compensation (spec §7.11.3.9/10) blending the above/left neighbours' predictions,
>   plus switchable interpolation-filter signalling. All 6 frames byte-exact.
> - **warped motion** 6-frame 64×64 rotating texture — `decode/warp_test.go`. LOCALWARP:
>   find_warp_samples + the least-squares affine fit (§7.11.3.8), setup_shear and the
>   warped sub-pel filter (§7.11.3.5), plus the someUseIntra sub-8×8 chroma path. All
>   6 frames byte-exact.
> - **global motion / inter-intra** 4-frame 64×64 rotating stream — `decode/globalmotion_test.go`.
>   Global-motion warp MC (GLOBALMV blocks, ROTZOOM model), inter-intra compound
>   (read_interintra_mode + the intra/inter blend), distance-weighted compound and uni-directional compound references. All 6 frames byte-exact.
>
> The full coefficient pipeline + inverse transforms are cross-checked against
> libaom (`av1_idct64`, `av1_inv_txfm2d_add_64x64`, and the smaller DCT/ADST/IDTX
> kernels).
| M6 | Loop filter (deblock) + CDEF + loop restoration | ✅ deblocking, CDEF, and loop restoration (both Wiener and self-guided/SGR) — all bit-exact against dav1d |
| M7 | Inter prediction: MV/ref-mv, OBMC, warped/global motion, compound, sub-pel MC, reference scaling, show_existing_frame | ✅ bit-exact so far: header + reference store + multi-frame driver, find_mv_stack (spatial), NEARESTMV/NEARMV/GLOBALMV/NEWMV + compound modes (read_mv), variable transform tree (var_tx), inter transform types, read_motion_mode, single-reference 8-tap sub-pel translational motion compensation, multi-reference CDF load/save (load_cdfs/save_cdfs + symbol-counter reset), masked compound prediction (WEDGE + DIFFWTD masks) with the compound extra-MV-search, switchable interpolation-filter signalling, OBMC, warped (local + global) motion compensation, inter-intra compound and distance-weighted compound, intra block copy, delta-Q/delta-LF + segmentation in inter frames. **all inter features bit-exact (validated across aomenc + SVT-AV1 fuzz)** |
| M8 | Film grain; superres; high bit depth; 4:2:2/4:4:4/mono; profiles 1–2 | ✅ all bit-exact with dav1d |

### Feature coverage

Every feature below is implemented and **bit-exact with dav1d**, cross-validated
across the official conformance vectors plus large randomized aomenc + SVT-AV1
fuzz corpora (two independent encoders):

**M7 — inter prediction ✅**
- ✅ Warped motion — `find_warp_samples` + least-squares affine fit + the warp/affine
      motion-comp process (§7.11.3.5-8) — LOCALWARP, bit-exact (`decode/warp_test.go`)
- ✅ sub-8x8 chroma prediction with intra neighbours (someUseIntra path)
- ✅ Global-motion warp motion compensation (ROTZOOM/affine, GLOBALMV blocks) — bit-exact
- ✅ Inter-intra compound prediction (`read_interintra_mode` + intra/inter blend, wedge + smooth masks)
- ✅ Distance-weighted compound (COMPOUND_DISTANCE) — the distance-weight combine
- ✅ Uni-directional compound references (`uni_comp_ref` context tree) — the global-motion
      6-frame stream is now fully byte-exact
- ✅ Temporal MV projection — `motion_field_estimation` + projection + temporal scan
      (§7.9 / §7.10.2.5-6); a 6-frame use_ref_frame_mvs stream is byte-exact (`decode/temporalmv_test.go`)
- ✅ tx_size context — use the block size (not the tx size) for inter neighbours (§9.3)
- ✅ Reference frame scaling — frame_size_with_refs (§5.9.7) + scaled motion
      compensation (§7.11.3.3), byte-exact (`decode/refscale_test.go`)
- ✅ Display-order output — `show_existing_frame` (§7.4) + load_previous (PrevGmParams /
      loop-filter deltas from the primary ref, §7.21), byte-exact (`decode/showexisting_test.go`)
- ✅ Intra block copy MC — reference dimensions use FrameWidth/Height (1:1 scale), bit-exact
      on non-mi-aligned widths (SVT-AV1 screen-content fuzz)
- ✅ WEDGE + DIFFWTD masked compound — validated bit-exact across aomenc/SVT-AV1 fuzz
      (`--enable-masked-comp` / `--enable-diff-wtd-comp`)
- ✅ Delta-Q / delta-LF + segmentation in inter frames — bit-exact, incl. per-segment
      ALT_Q/ALT_LF, **SEG_LVL_SKIP / SEG_LVL_REF_FRAME / SEG_LVL_GLOBALMV** forced features,
      temporal segment-id prediction, and `!update_data` feature inheritance (§5.9.14)

**M8 — final coverage ✅**
- ✅ Film grain synthesis (§7.18.3) — AR grain template, scaling LUT, overlap
      blending, per-pixel noise blend; byte-exact with dav1d (`decode/filmgrain_test.go`)
- ✅ Superres (§7.16) — partial partition at frame edge (split_or_horz/vert gathered CDF),
      horizontal upscaling, reference scaling from upscaled refs; byte-exact (`decode/superres_test.go`)
- ✅ 10-bit / 12-bit (high bit depth) — bit-depth-indexed quantizer tables, byte-exact
      with dav1d (`decode/highbitdepth_test.go`)
- ✅ 4:2:2 / 4:4:4 chroma subsampling + monochrome (4:0:0), byte-exact (`decode/chroma_test.go`)
- ✅ Multi-tile (2x2 tiles), per-tile CDF + context_update_tile_id (`decode/multitile_test.go`)
- ✅ Intra block copy (screen content) — block-vector copy from the current frame
      (`decode/intrabc_test.go`)
- ✅ Palette mode (screen content) — palette cache/colors + wavefront color map
      (`decode/palette_test.go`); official intrabc+palette conformance vector byte-exact
- ✅ Quantizer matrices (`using_qmatrix`, §7.12.3) — full `Quantizer_Matrix[15][2][3344]`
      table + per-coefficient dequant scaling; byte-exact (8/10/12-bit, + segmentation)
- ✅ Profiles 1 and 2 (validated by the 4:4:4 / 4:2:2 / 12-bit streams)
- ✅ Lossless (Walsh-Hadamard transform) — `InverseWHT2D` + lossless `is_cfl_allowed` and
      4x4 tx forcing; byte-exact on the official quantizer-00 conformance vector
- ✅ Official 8-bit conformance vectors — quantizer levels, sizes, cdfupdate, mfmv, OBMC;
      byte-exact with dav1d (`decode/obmc_conformance_test.go`). The OBMC predictor uses each
      neighbour's interpolation filter (not the current block's).

Each milestone is validated bit-exactly: build dav1d, dump intermediate state,
compare. Method mirrors the libtheora/go-mpeg4 ports.

## Layout
- `obu/` — OBU framing + LEB128 (M0).
- `msac/` — symbol (CDF range) decoder + matching od_ec encoder (M1).
- `bits/` — big-endian bit reader: f(n)/uvlc/le/leb128/su/ns (M2).
- `header/` — sequence header + uncompressed frame header parsing (M2, intra path).
- `tile/` — tile group OBU split + per-tile symbol decoder init (M3).
- `predict/` — block geometry + reference-sample construction (`PredictIntra`) +
  DC/Paeth/Smooth predictors + plane buffers (M4). Wired into the decoder with
  BlockDecoded tracking for above-right/below-left availability.
- `cdf/` — default CDF tables, generated from the spec markdown (M3+).
- `transform/` — inverse DCT + ADST + identity transforms + 2D inverse transform
  (M5), all cross-checked bit-exactly against libaom.
- `decode/` — the decode pipeline: tile/partition/mode-info/residual/coeff +
  reconstruction (M3–M5); deblock/CDEF/loop-restoration/SGR (M6); the inter path
  (M7) — reference store, find_mv_stack, inter mode info, compound, masked
  compound (wedge/diffwtd), var-tx, sub-pel motion compensation and the
  multi-frame sequence driver.

Per-block ground truth for M3–M5 comes from libaom's `inspect` tool
(`-DCONFIG_INSPECTION=1 -DCONFIG_ACCOUNTING=1`), which dumps partition / mode /
skip / transform maps as JSON alongside the dav1d final-pixel reference.

## Verification

Reference oracle: `aomenc` (AV1 reference encoder) generates IVF test bitstreams,
`dav1d` decodes them as the bit-exact ground truth, and libaom's `inspect` dumps
per-block state as JSON. Header parsing is checked against real aomenc streams
across resolutions, profiles (0/1, 4:2:0/4:4:4), lossless vs lossy, multi-tile
and loop-restoration configurations. The regression tests embed real temporal-unit
bytes and assert the SHA256 of every reconstructed plane against the dav1d output.

## Fuzz / bug-hunt campaign

Continuous differential fuzzing: random videos are encoded by **two independent
encoders** (`aomenc` + `SvtAv1EncApp`) with randomized tools/sizes/bit-depths, then
decoded by both `go-av1` and `dav1d` and compared bit-for-bit. A stream only counts
as a test if `dav1d` decodes it with **zero errors** (non-conformant encoder output
is skipped, not flagged).

Test campaigns (latest status):

- ✅ Official conformance vectors — **242/242** (complete `av1-1-*` set: 8/10-bit at every
  quantizer, sizes, cdfupdate, mv/mfmv, intra_only, svc, film_grain, monochrome) bit-exact vs dav1d
- ✅ aomenc randomized fuzz — content/size/depth/tool sweeps, **all pass**
- ✅ SVT-AV1 fuzz (second encoder, different bitstream paths) — **80/80**
- ✅ Advanced inter tools (OBMC / warp / masked / diffwtd / interintra / dual-filter / global-motion) — **84/84**
- ✅ Screen content (palette + intra block copy), incl. non-mi-aligned sizes
- ✅ Quantizer matrices, segmentation, delta-Q / delta-LF in inter frames
- ✅ SEG_LVL_SKIP / SEG_LVL_REF_FRAME (forced segment features) — targeted vectors via a
  libaom ROI encoder (aomenc/SVT never emit them); `decode/seg_lvl_test.go`
- ✅ Per-segment qindex / lossless with `base_q_idx=0` (mixed lossless + lossy segments):
  intra_tx_type gating and tx-size / tx-type / inverse-transform use the per-segment value,
  not the frame-level one (`decode/bug8_lossless_seg_test.go`) — **1691/1691** ROI fuzz
- ✅ HD 720p/1080p + long-GOP sweeps — **30/30**
- ✅ Edge-case dimensions (tiny / thin: 16×16, 16×240, 240×16) — **72/72**
- ✅ Resize / superres / error-resilient (per-frame resolution changes) — **32/32**

Bugs found by fuzzing and fixed (all now bit-exact):

- ✅ `delta_lf` — per-superblock loop-filter deltas
- ✅ Segmentation in inter frames (spatial + temporal + ALT_Q/ALT_LF)
- ✅ Segmentation `FeatureData` inheritance when `!update_data` (§5.9.14)
- ✅ Quantizer matrices (`using_qmatrix`) dequant scaling
- ✅ SB-aligned plane allocation (edge transforms past the mi-grid)
- ✅ Intra block copy MC scaling on non-mi-aligned frame widths
