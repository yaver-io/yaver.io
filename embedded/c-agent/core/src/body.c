#include "yvr/body.h"
#include "yvr/cbor.h"

#include <string.h>

/* Encode-time helper: write the protocol_version field's value
 * given the body's declared protocol_version. Centralized so a
 * future bump only edits one place. */
static yvr_status_t w_protocol_version(yvr_cbor_w_t *w, uint32_t v)
{
    return yvr_cbor_w_uint(w, (uint64_t)v);
}

/* ── HELLO ──────────────────────────────────────────────────── */

yvr_status_t yvr_hello_encode(const yvr_hello_t *h,
                              uint8_t           *buf,
                              size_t             cap,
                              size_t            *out_len)
{
    if (h == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (h->role == NULL || h->role_len == 0) {
        return YVR_E_INVALID_ARG;
    }
    if (h->agent_version == NULL && h->agent_version_len != 0) {
        return YVR_E_INVALID_ARG;
    }

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    /* Map size: 2 mandatory keys + optional agent_version. */
    const bool has_av = (h->agent_version != NULL && h->agent_version_len > 0);
    yvr_cbor_w_map_begin(&w, has_av ? 3 : 2);

    /* "v" → protocol_version (key length 1; smallest CBOR head). */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, h->protocol_version);

    /* "role" → text */
    yvr_cbor_w_text(&w, "role", 4);
    yvr_cbor_w_text(&w, h->role, h->role_len);

    /* "agent_version" → text (optional) */
    if (has_av) {
        yvr_cbor_w_text(&w, "agent_version", 13);
        yvr_cbor_w_text(&w, h->agent_version, h->agent_version_len);
    }

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) {
        return s;
    }
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_hello_decode(const uint8_t *buf,
                              size_t         n,
                              yvr_hello_t   *out)
{
    if (buf == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    /* Zero-init so unset optional fields read back NULL/0. */
    *out = (yvr_hello_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) {
        return s;
    }

    bool seen_v    = false;
    bool seen_role = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) {
            return s;
        }
        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) {
                return s;
            }
            if (v > 0xFFFFFFFFu) {
                return YVR_E_BAD_FRAME;
            }
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "role", 4) == 0) {
            s = yvr_cbor_r_text(&r, &out->role, &out->role_len);
            if (s != YVR_OK) {
                return s;
            }
            seen_role = true;
            continue;
        }
        if (kn == 13 && memcmp(k, "agent_version", 13) == 0) {
            s = yvr_cbor_r_text(&r, &out->agent_version, &out->agent_version_len);
            if (s != YVR_OK) {
                return s;
            }
            continue;
        }
        /* Unknown field — skip for forward compat. */
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) {
            return s;
        }
    }

    if (!seen_v || !seen_role) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── HEARTBEAT ──────────────────────────────────────────────── */

yvr_status_t yvr_heartbeat_encode(const yvr_heartbeat_t *h,
                                  uint8_t               *buf,
                                  size_t                 cap,
                                  size_t                *out_len)
{
    if (h == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (h->signature == NULL && h->signature_len != 0) {
        return YVR_E_INVALID_ARG;
    }

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    const bool has_sig = (h->signature != NULL && h->signature_len > 0);
    yvr_cbor_w_map_begin(&w, has_sig ? 3 : 2);

    /* CTAP2 order: "v" (head 0x61) < "now_ms" (head 0x66) < "signature" (0x69). */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, h->protocol_version);

    yvr_cbor_w_text(&w, "now_ms", 6);
    yvr_cbor_w_uint(&w, h->now_ms);

    if (has_sig) {
        yvr_cbor_w_text(&w, "signature", 9);
        yvr_cbor_w_bytes(&w, h->signature, h->signature_len);
    }

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) {
        return s;
    }
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_heartbeat_decode(const uint8_t   *buf,
                                  size_t           n,
                                  yvr_heartbeat_t *out)
{
    if (buf == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    *out = (yvr_heartbeat_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) {
        return s;
    }

    bool seen_v      = false;
    bool seen_now_ms = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) {
            return s;
        }
        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) {
                return s;
            }
            if (v > 0xFFFFFFFFu) {
                return YVR_E_BAD_FRAME;
            }
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 6 && memcmp(k, "now_ms", 6) == 0) {
            s = yvr_cbor_r_uint(&r, &out->now_ms);
            if (s != YVR_OK) {
                return s;
            }
            seen_now_ms = true;
            continue;
        }
        if (kn == 9 && memcmp(k, "signature", 9) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->signature, &out->signature_len);
            if (s != YVR_OK) {
                return s;
            }
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) {
            return s;
        }
    }

    if (!seen_v || !seen_now_ms) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}
