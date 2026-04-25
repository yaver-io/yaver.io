/* Yaver c-agent — wire frame header codec.
 *
 * Encodes and decodes the fixed 9-byte HTTP/2-style frame header used
 * by every c-agent transport. Bytes-on-the-wire (big-endian):
 *
 *   +---------+--------+--------+----------------+
 *   | length  | type   | flags  |   stream_id    |
 *   | 24 bits | 8 bits | 8 bits |   32 bits      |
 *   +---------+--------+--------+----------------+
 *
 * The codec is pure: no allocation, no I/O, no global state. Callers
 * own all buffers. Length must fit in 24 bits (see
 * YVR_FRAME_MAX_PAYLOAD). Stream id 0 is connection-scoped; non-zero
 * ids are owned by the side that opened them.
 */

#ifndef YVR_FRAME_H
#define YVR_FRAME_H

#include "status.h"
#include "types.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct yvr_frame_header {
    uint32_t length;     /* 24-bit; <= YVR_FRAME_MAX_PAYLOAD */
    uint8_t  type;       /* yvr_frame_type_t value */
    uint8_t  flags;      /* bitfield of yvr_frame_flag_t */
    uint32_t stream_id;  /* 32-bit identifier, 0 = connection-scoped */
} yvr_frame_header_t;

/* Encode `hdr` into the first YVR_FRAME_HEADER_SIZE bytes of `out`.
 *
 * Returns:
 *   YVR_OK                   on success
 *   YVR_E_INVALID_ARG        if `hdr` or `out` is NULL
 *   YVR_E_BUFFER_TOO_SMALL   if `out_len` < YVR_FRAME_HEADER_SIZE
 *   YVR_E_PAYLOAD_TOO_LARGE  if `hdr->length` > YVR_FRAME_MAX_PAYLOAD
 */
yvr_status_t yvr_frame_header_encode(const yvr_frame_header_t *hdr,
                                     uint8_t                  *out,
                                     size_t                    out_len);

/* Decode a frame header from the first YVR_FRAME_HEADER_SIZE bytes of
 * `in` into `*out_hdr`. The buffer may carry additional payload bytes
 * after the header; only the header bytes are read.
 *
 * Returns:
 *   YVR_OK              on success
 *   YVR_E_INVALID_ARG   if `in` or `out_hdr` is NULL
 *   YVR_E_TRUNCATED     if `in_len` < YVR_FRAME_HEADER_SIZE
 */
yvr_status_t yvr_frame_header_decode(const uint8_t            *in,
                                     size_t                    in_len,
                                     yvr_frame_header_t       *out_hdr);

#ifdef __cplusplus
}
#endif

#endif /* YVR_FRAME_H */
