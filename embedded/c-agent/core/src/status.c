#include "yvr/status.h"

const char *yvr_status_str(yvr_status_t s)
{
    switch (s) {
    case YVR_OK:                  return "ok";
    case YVR_E_INVALID_ARG:       return "invalid argument";
    case YVR_E_BUFFER_TOO_SMALL:  return "buffer too small";
    case YVR_E_PAYLOAD_TOO_LARGE: return "payload too large";
    case YVR_E_TRUNCATED:         return "truncated";
    case YVR_E_BAD_FRAME:         return "bad frame";
    case YVR_E_INTERNAL:          return "internal error";
    }
    return "unknown";
}
