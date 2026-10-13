package convert

import (
	"bytes"
	"math/bits"
	"testing"
)

// Synthetic Dolby Vision RPUs, for the real-encode tests (dv_it_test.go). dovi_tool can
// generate profile 5 and 8 RPUs but not 7 (a dual-layer disc's), so the test writes one
// itself: the RPU syntax (the same one dovi_tool and ffmpeg parse) with a residual — the
// non-linear quantiser data a full enhancement layer carries — and a plain identity mapping.
// The same writer makes a bare profile 8.1 RPU with no extension blocks, which ffmpeg's
// rewrite mangles: the case the output gate must catch. Never anything from a real disc.

// The RPU's CRC is CRC-32/MPEG-2; this is its published check value.
func TestRPUCRC(t *testing.T) {
	if got := crc32MPEG2([]byte("123456789")); got != 0x0376E6E7 {
		t.Fatalf("crc = %#x", got)
	}
}

// bitWriter writes MSB-first, with the Exp-Golomb codes the RPU uses.
type bitWriter struct {
	buf  []byte
	nbit int
}

func (w *bitWriter) bit(b bool) {
	if w.nbit%8 == 0 {
		w.buf = append(w.buf, 0)
	}
	if b {
		w.buf[len(w.buf)-1] |= 0x80 >> (w.nbit % 8)
	}
	w.nbit++
}

func (w *bitWriter) u(n int, v uint64) {
	for i := n - 1; i >= 0; i-- {
		w.bit(v>>uint(i)&1 == 1)
	}
}

func (w *bitWriter) ue(v uint64) {
	n := bits.Len64(v + 1)
	w.u(n-1, 0)
	w.u(n, v+1)
}

func (w *bitWriter) se(v int64) {
	if v > 0 {
		w.ue(uint64(2*v - 1))
	} else {
		w.ue(uint64(-2 * v))
	}
}

func (w *bitWriter) align() {
	for w.nbit%8 != 0 {
		w.bit(false)
	}
}

// crc32MPEG2 is the RPU's CRC: CRC-32/MPEG-2 (polynomial 0x04C11DB7, not reflected,
// initial value all ones, no final XOR).
func crc32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// syntheticRPU is one RPU's payload: the 0x19 prefix, the RPU, its CRC and the 0x80
// terminator (no NAL header, no emulation prevention — rpuFile adds that). fel writes a
// profile 7 FEL RPU (with a residual), else a profile 8.1 one; extBlocks adds the L1/L5/L6
// extension blocks every real grade carries — without them it's a bare, out-of-spec RPU.
func syntheticRPU(fel, extBlocks, sceneCut bool) []byte {
	const denom = 23
	w := &bitWriter{}
	// rpu_data_header
	w.u(6, 2)    // rpu_type
	w.u(11, 18)  // rpu_format
	w.u(4, 1)    // vdr_rpu_profile
	w.u(4, 0)    // vdr_rpu_level
	w.bit(true)  // vdr_seq_info_present_flag
	w.bit(false) // chroma_resampling_explicit_filter_flag
	w.u(2, 0)    // coefficient_data_type: fixed point
	w.ue(denom)  // coefficient_log2_denom
	w.u(2, 1)    // vdr_rpu_normalized_idc
	w.bit(false) // bl_video_full_range_flag
	w.ue(2)      // bl_bit_depth_minus8
	w.ue(2)      // el_bit_depth_minus8
	w.ue(4)      // vdr_bit_depth_minus8
	w.bit(false) // spatial_resampling_filter_flag
	w.u(3, 0)    // reserved_zero_3bits
	w.bit(fel)   // el_spatial_resampling_filter_flag: a 4K FEL is a 1080p layer upscaled
	w.bit(!fel)  // disable_residual_flag: with a residual it's profile 7
	w.bit(true)  // vdr_dm_metadata_present_flag
	w.bit(false) // use_prev_vdr_rpu_flag
	w.ue(0)      // vdr_rpu_id
	w.ue(0)      // mapping_color_space
	w.ue(0)      // mapping_chroma_format_idc
	for c := 0; c < 3; c++ {
		w.ue(0)       // num_pivots_minus2
		w.u(10, 0)    // pivot 0
		w.u(10, 1023) // pivot 1 (a delta)
	}
	if fel {
		w.u(3, 0)     // nlq_method_idc: linear dead zone
		w.u(10, 0)    // nlq pivot 0
		w.u(10, 1023) // nlq pivot 1
	}
	w.ue(0) // num_x_partitions_minus1
	w.ue(0) // num_y_partitions_minus1
	// rpu_data_mapping: one identity polynomial (0 + 1·x) per component
	for c := 0; c < 3; c++ {
		w.ue(0)      // mapping_idc: polynomial
		w.ue(0)      // poly_order_minus1: linear
		w.bit(false) // linear_interp_flag
		w.se(0)
		w.u(denom, 0)
		w.se(1)
		w.u(denom, 0)
	}
	// rpu_data_nlq: a small, plausible residual
	for c := 0; fel && c < 3; c++ {
		w.u(10, 512)             // nlq_offset (el bit depth)
		w.ue(1)                  // vdr_in_max_int
		w.u(denom, 0)            // vdr_in_max
		w.ue(0)                  // linear_deadzone_slope_int
		w.u(denom, 1<<(denom-1)) // linear_deadzone_slope 0.5
		w.ue(0)                  // linear_deadzone_threshold_int
		w.u(denom, 0)            // linear_deadzone_threshold
	}
	// vdr_dm_data: the standard BT.2020 PQ display-management block
	w.ue(0) // affected_dm_metadata_id
	w.ue(0) // current_dm_metadata_id
	if sceneCut {
		w.ue(1) // scene_refresh_flag
	} else {
		w.ue(0)
	}
	for _, v := range []int64{9574, 0, 13802, 9574, -1540, -5348, 9574, 17610, 0} {
		w.u(16, uint64(uint16(int16(v)))) // ycc_to_rgb_coef
	}
	for _, v := range []uint64{16777216, 134217728, 134217728} {
		w.u(32, v) // ycc_to_rgb_offset
	}
	for _, v := range []int64{7222, 8771, 390, 2654, 12430, 1300, 0, 422, 15962} {
		w.u(16, uint64(uint16(int16(v)))) // rgb_to_lms_coef
	}
	w.u(16, 65535) // signal_eotf
	w.u(16, 0)     // signal_eotf_param0
	w.u(16, 0)     // signal_eotf_param1
	w.u(32, 0)     // signal_eotf_param2
	w.u(5, 12)     // signal_bit_depth
	w.u(2, 0)      // signal_color_space
	w.u(2, 0)      // signal_chroma_format
	w.u(2, 1)      // signal_full_range_flag
	w.u(12, 7)     // source_min_pq
	w.u(12, 3079)  // source_max_pq
	w.u(10, 42)    // source_diagonal
	if !extBlocks {
		w.ue(0) // num_ext_blocks
		return finishRPU(w)
	}
	// CM v2.9 extension blocks, as every real grade carries: L1 (the frame's PQ range), L5
	// (no letterbox), L6 (the HDR10 mastering figures). Each is its length in bytes, its
	// level, the fields, then zero bits to the stated length.
	w.ue(3) // num_ext_blocks
	w.align()
	block := func(level uint64, length int, fields func()) {
		w.ue(uint64(length))
		w.u(8, level)
		start := w.nbit
		fields()
		for w.nbit-start < length*8 {
			w.bit(false)
		}
	}
	block(1, 5, func() { w.u(12, 0); w.u(12, 2081); w.u(12, 1229) })
	block(5, 7, func() { w.u(13, 0); w.u(13, 0); w.u(13, 0); w.u(13, 0) })
	block(6, 8, func() { w.u(16, 1000); w.u(16, 1); w.u(16, 1000); w.u(16, 400) })
	return finishRPU(w)
}

// finishRPU byte-aligns the RPU and frames it: prefix, CRC (over the RPU itself), terminator.
func finishRPU(w *bitWriter) []byte {
	w.align()
	crc := crc32MPEG2(w.buf)
	out := append([]byte{0x19}, w.buf...)
	out = append(out, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return append(out, 0x80)
}

// rpuFile is an RPU file as dovi_tool reads one: each RPU after a start code, with
// emulation prevention.
func rpuFile(rpus [][]byte) []byte {
	var b bytes.Buffer
	for _, r := range rpus {
		b.Write([]byte{0, 0, 0, 1})
		zeros := 0
		for _, c := range r {
			if zeros >= 2 && c <= 3 {
				b.WriteByte(3)
				zeros = 0
			}
			b.WriteByte(c)
			if c == 0 {
				zeros++
			} else {
				zeros = 0
			}
		}
	}
	return b.Bytes()
}
