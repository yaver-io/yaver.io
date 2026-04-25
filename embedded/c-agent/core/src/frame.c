#include "yvr/frame.h"

/* Big-endian frame header layout:
 *
 *   byte 0..2  length      (24-bit unsigned)
 *   byte 3     type        (8-bit unsigned)
 *   byte 4     flags       (8-bit bitfield)
 *   byte 5..8  stream_id   (32-bit unsigned)
 *
 * Hand-rolled byte ops instead of htonl/htons so the codec works on
 * any host without pulling in <arpa/inet.h> (no libc dependency
 * beyond <stdint.h>/<stddef.h>) and behaves identically across
 * little-/big-endian targets. */

yvr_status_t yvr_frame_header_encode(const yvr_frame_header_t *hdr,
                                     uint8_t                  *out,
                                     size_t                    out_len)
{
    if (hdr == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (out_len < (size_t)YVR_FRAME_HEADER_SIZE) {
        return YVR_E_BUFFER_TOO_SMALL;
    }
    if (hdr->length > YVR_FRAME_MAX_PAYLOAD) {
        return YVR_E_PAYLOAD_TOO_LARGE;
    }

    out[0] = (uint8_t)((hdr->length >> 16) & 0xFFu);
    out[1] = (uint8_t)((hdr->length >>  8) & 0xFFu);
    out[2] = (uint8_t)((hdr->length >>  0) & 0xFFu);
    out[3] = hdr->type;
    out[4] = hdr->flags;
    out[5] = (uint8_t)((hdr->stream_id >> 24) & 0xFFu);
    out[6] = (uint8_t)((hdr->stream_id >> 16) & 0xFFu);
    out[7] = (uint8_t)((hdr->stream_id >>  8) & 0xFFu);
    out[8] = (uint8_t)((hdr->stream_id >>  0) & 0xFFu);

    return YVR_OK;
}

yvr_status_t yvr_frame_header_decode(const uint8_t            *in,
                                     size_t                    in_len,
                                     yvr_frame_header_t       *out_hdr)
{
    if (in == NULL || out_hdr == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (in_len < (size_t)YVR_FRAME_HEADER_SIZE) {
        return YVR_E_TRUNCATED;
    }

    out_hdr->length =
        ((uint32_t)in[0] << 16) |
        ((uint32_t)in[1] <<  8) |
        ((uint32_t)in[2] <<  0);
    out_hdr->type      = in[3];
    out_hdr->flags     = in[4];
    out_hdr->stream_id =
        ((uint32_t)in[5] << 24) |
        ((uint32_t)in[6] << 16) |
        ((uint32_t)in[7] <<  8) |
        ((uint32_t)in[8] <<  0);

    /* The 24-bit length cannot overflow YVR_FRAME_MAX_PAYLOAD by
     * construction (we only read three bytes). Nothing else to
     * validate at the header layer — body validation belongs to the
     * per-type decoders. */

    return YVR_OK;
}
