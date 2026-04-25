/* Yaver c-agent — primitive types and frame constants.
 *
 * The wire layer uses a fixed 9-byte frame header (HTTP/2-style)
 * carrying a 24-bit payload length, an 8-bit type, an 8-bit flags
 * field, and a 32-bit stream id. See docs/c-agent-architecture.md §6
 * for the full protocol.
 *
 * This header is part of the runtime's public C ABI. Stays
 * conservative — fixed-width integer types only, no platform headers
 * beyond <stdint.h>/<stddef.h>, no exposure of internal structs.
 */

#ifndef YVR_TYPES_H
#define YVR_TYPES_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Wire frame types. Numeric values are stable; new types append. */
typedef enum yvr_frame_type {
    YVR_FRAME_HELLO         = 0x01,
    YVR_FRAME_AUTH          = 0x02,
    YVR_FRAME_AUTHRSP       = 0x03,
    YVR_FRAME_ATTEST        = 0x04,
    YVR_FRAME_INVOKE        = 0x05,
    YVR_FRAME_TOOL_RSP      = 0x06,
    YVR_FRAME_NEED          = 0x07,
    YVR_FRAME_MODULE        = 0x08,
    YVR_FRAME_STREAM_CHUNK  = 0x09,
    YVR_FRAME_EVENT         = 0x0A,
    YVR_FRAME_HEARTBEAT     = 0x0B,
    YVR_FRAME_APPROVAL_REQ  = 0x0C,
    YVR_FRAME_APPROVAL_RSP  = 0x0D,
    YVR_FRAME_ERROR         = 0x0E,
    YVR_FRAME_WINDOW_UPDATE = 0x0F,
    YVR_FRAME_KILL          = 0x10
} yvr_frame_type_t;

/* Frame flag bits. ORable. Reserved bits MUST be zero on send and
 * SHOULD be ignored on receive so future flags don't break peers. */
typedef enum yvr_frame_flag {
    YVR_FLAG_END_STREAM = 0x01,
    YVR_FLAG_ACK        = 0x02,
    YVR_FLAG_COMPRESSED = 0x04
} yvr_frame_flag_t;

/* Wire-format constants. */
#define YVR_FRAME_HEADER_SIZE 9
#define YVR_FRAME_MAX_PAYLOAD ((uint32_t)0x00FFFFFFu) /* 24-bit cap */

#ifdef __cplusplus
}
#endif

#endif /* YVR_TYPES_H */
