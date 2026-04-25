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

/* ── AUTH ───────────────────────────────────────────────────── */

yvr_status_t yvr_auth_encode(const yvr_auth_t *a,
                             uint8_t          *buf,
                             size_t            cap,
                             size_t           *out_len)
{
    if (a == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (a->nonce == NULL || a->nonce_len == 0) {
        return YVR_E_INVALID_ARG;
    }

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    yvr_cbor_w_map_begin(&w, 3);

    /* CTAP2 order: "v" (0x61) < "nonce" (0x65) < "signed_now_ms" (0x6d). */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, a->protocol_version);

    yvr_cbor_w_text(&w, "nonce", 5);
    yvr_cbor_w_bytes(&w, a->nonce, a->nonce_len);

    yvr_cbor_w_text(&w, "signed_now_ms", 13);
    yvr_cbor_w_uint(&w, a->signed_now_ms);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) {
        return s;
    }
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_auth_decode(const uint8_t *buf,
                             size_t         n,
                             yvr_auth_t    *out)
{
    if (buf == NULL || out == NULL) {
        return YVR_E_INVALID_ARG;
    }
    *out = (yvr_auth_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) {
        return s;
    }

    bool seen_v = false, seen_nonce = false, seen_now = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 5 && memcmp(k, "nonce", 5) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->nonce, &out->nonce_len);
            if (s != YVR_OK) return s;
            seen_nonce = true;
            continue;
        }
        if (kn == 13 && memcmp(k, "signed_now_ms", 13) == 0) {
            s = yvr_cbor_r_uint(&r, &out->signed_now_ms);
            if (s != YVR_OK) return s;
            seen_now = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_nonce || !seen_now) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── AUTHRSP ────────────────────────────────────────────────── */

yvr_status_t yvr_authrsp_encode(const yvr_authrsp_t *r,
                                uint8_t             *buf,
                                size_t               cap,
                                size_t              *out_len)
{
    if (r == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (r->sig == NULL || r->sig_len == 0) return YVR_E_INVALID_ARG;
    if (r->nonce == NULL || r->nonce_len == 0) return YVR_E_INVALID_ARG;
    if (r->device_cert == NULL || r->device_cert_len == 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    yvr_cbor_w_map_begin(&w, 4);

    /* CTAP2 order: "v" (0x61) < "sig" (0x63) < "nonce" (0x65) <
     * "device_cert" (0x6b). */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, r->protocol_version);

    yvr_cbor_w_text(&w, "sig", 3);
    yvr_cbor_w_bytes(&w, r->sig, r->sig_len);

    yvr_cbor_w_text(&w, "nonce", 5);
    yvr_cbor_w_bytes(&w, r->nonce, r->nonce_len);

    yvr_cbor_w_text(&w, "device_cert", 11);
    yvr_cbor_w_bytes(&w, r->device_cert, r->device_cert_len);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) {
        return s;
    }
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_authrsp_decode(const uint8_t *buf,
                                size_t         n,
                                yvr_authrsp_t *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_authrsp_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_sig = false, seen_nonce = false, seen_cert = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 3 && memcmp(k, "sig", 3) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->sig, &out->sig_len);
            if (s != YVR_OK) return s;
            seen_sig = true;
            continue;
        }
        if (kn == 5 && memcmp(k, "nonce", 5) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->nonce, &out->nonce_len);
            if (s != YVR_OK) return s;
            seen_nonce = true;
            continue;
        }
        if (kn == 11 && memcmp(k, "device_cert", 11) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->device_cert, &out->device_cert_len);
            if (s != YVR_OK) return s;
            seen_cert = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_sig || !seen_nonce || !seen_cert) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── ATTEST ─────────────────────────────────────────────────── */

yvr_status_t yvr_attest_encode(const yvr_attest_t *a,
                               uint8_t            *buf,
                               size_t              cap,
                               size_t             *out_len)
{
    if (a == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (a->arch == NULL || a->arch_len == 0)     return YVR_E_INVALID_ARG;
    if (a->libc == NULL || a->libc_len == 0)     return YVR_E_INVALID_ARG;
    if (a->kernel == NULL || a->kernel_len == 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    yvr_cbor_w_map_begin(&w, 7);

    /* CTAP2 order:
     *   "v"               (0x61)
     *   "arch"            (0x64-61)
     *   "libc"            (0x64-6c)
     *   "kernel"          (0x66)
     *   "capabilities"    (0x6c)
     *   "ebpf_supported"  (0x6e)
     *   "cache_quota_bytes" (0x71) */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, a->protocol_version);

    yvr_cbor_w_text(&w, "arch", 4);
    yvr_cbor_w_text(&w, a->arch, a->arch_len);

    yvr_cbor_w_text(&w, "libc", 4);
    yvr_cbor_w_text(&w, a->libc, a->libc_len);

    yvr_cbor_w_text(&w, "kernel", 6);
    yvr_cbor_w_text(&w, a->kernel, a->kernel_len);

    yvr_cbor_w_text(&w, "capabilities", 12);
    yvr_cbor_w_array_begin(&w, a->capabilities_count);
    for (size_t i = 0; i < a->capabilities_count; i++) {
        const char *cap_name = a->capabilities[i];
        if (cap_name == NULL) {
            return YVR_E_INVALID_ARG;
        }
        yvr_cbor_w_text(&w, cap_name, strlen(cap_name));
    }

    yvr_cbor_w_text(&w, "ebpf_supported", 14);
    yvr_cbor_w_bool(&w, a->ebpf_supported);

    yvr_cbor_w_text(&w, "cache_quota_bytes", 17);
    yvr_cbor_w_uint(&w, a->cache_quota_bytes);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_attest_decode(const uint8_t  *buf,
                               size_t          n,
                               yvr_attest_t   *out,
                               const char    **out_capabilities,
                               size_t         *out_capabilities_lens,
                               size_t          capabilities_cap)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    /* `out_capabilities` may be NULL — see header doc. */
    *out = (yvr_attest_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_arch = false, seen_libc = false, seen_kernel = false;
    bool overflow = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "arch", 4) == 0) {
            s = yvr_cbor_r_text(&r, &out->arch, &out->arch_len);
            if (s != YVR_OK) return s;
            seen_arch = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "libc", 4) == 0) {
            s = yvr_cbor_r_text(&r, &out->libc, &out->libc_len);
            if (s != YVR_OK) return s;
            seen_libc = true;
            continue;
        }
        if (kn == 6 && memcmp(k, "kernel", 6) == 0) {
            s = yvr_cbor_r_text(&r, &out->kernel, &out->kernel_len);
            if (s != YVR_OK) return s;
            seen_kernel = true;
            continue;
        }
        if (kn == 12 && memcmp(k, "capabilities", 12) == 0) {
            size_t arr_n;
            s = yvr_cbor_r_array_begin(&r, &arr_n);
            if (s != YVR_OK) return s;
            out->capabilities_count = arr_n;
            for (size_t j = 0; j < arr_n; j++) {
                const char *cap_p;
                size_t      cap_n;
                s = yvr_cbor_r_text(&r, &cap_p, &cap_n);
                if (s != YVR_OK) return s;
                if (out_capabilities != NULL && j < capabilities_cap) {
                    out_capabilities[j] = cap_p;
                    if (out_capabilities_lens != NULL) {
                        out_capabilities_lens[j] = cap_n;
                    }
                } else if (j >= capabilities_cap && out_capabilities != NULL) {
                    overflow = true;
                }
            }
            continue;
        }
        if (kn == 14 && memcmp(k, "ebpf_supported", 14) == 0) {
            s = yvr_cbor_r_bool(&r, &out->ebpf_supported);
            if (s != YVR_OK) return s;
            continue;
        }
        if (kn == 17 && memcmp(k, "cache_quota_bytes", 17) == 0) {
            s = yvr_cbor_r_uint(&r, &out->cache_quota_bytes);
            if (s != YVR_OK) return s;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_arch || !seen_libc || !seen_kernel) {
        return YVR_E_BAD_FRAME;
    }
    return overflow ? YVR_E_BUFFER_TOO_SMALL : YVR_OK;
}

/* ── ERROR ──────────────────────────────────────────────────── */

yvr_status_t yvr_error_encode(const yvr_error_t *e,
                              uint8_t           *buf,
                              size_t             cap,
                              size_t            *out_len)
{
    if (e == NULL || buf == NULL || out_len == NULL) return YVR_E_INVALID_ARG;
    if (e->message == NULL && e->message_len != 0) return YVR_E_INVALID_ARG;
    if (e->context == NULL && e->context_len != 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    /* Map size: v + code + message are mandatory; context + stream_id optional. */
    const bool has_ctx    = (e->context   != NULL && e->context_len   > 0);
    const bool has_stream = (e->stream_id != 0);
    size_t map_n = 3 + (has_ctx ? 1 : 0) + (has_stream ? 1 : 0);

    yvr_cbor_w_map_begin(&w, map_n);

    /* CTAP2 order:
     *   "v" (0x61) < "code" (0x64) < "context" (0x67-63) <
     *   "message" (0x67-6d) < "stream_id" (0x69) */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, e->protocol_version);

    yvr_cbor_w_text(&w, "code", 4);
    yvr_cbor_w_int(&w, (int64_t)e->code);

    if (has_ctx) {
        yvr_cbor_w_text(&w, "context", 7);
        yvr_cbor_w_text(&w, e->context, e->context_len);
    }

    yvr_cbor_w_text(&w, "message", 7);
    yvr_cbor_w_text(&w, e->message != NULL ? e->message : "", e->message_len);

    if (has_stream) {
        yvr_cbor_w_text(&w, "stream_id", 9);
        yvr_cbor_w_uint(&w, e->stream_id);
    }

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_error_decode(const uint8_t *buf,
                              size_t         n,
                              yvr_error_t   *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_error_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_code = false, seen_msg = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "code", 4) == 0) {
            int64_t v;
            s = yvr_cbor_r_int(&r, &v);
            if (s != YVR_OK) return s;
            if (v < INT32_MIN || v > INT32_MAX) return YVR_E_BAD_FRAME;
            out->code = (int32_t)v;
            seen_code = true;
            continue;
        }
        if (kn == 7 && memcmp(k, "context", 7) == 0) {
            s = yvr_cbor_r_text(&r, &out->context, &out->context_len);
            if (s != YVR_OK) return s;
            continue;
        }
        if (kn == 7 && memcmp(k, "message", 7) == 0) {
            s = yvr_cbor_r_text(&r, &out->message, &out->message_len);
            if (s != YVR_OK) return s;
            seen_msg = true;
            continue;
        }
        if (kn == 9 && memcmp(k, "stream_id", 9) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->stream_id = (uint32_t)v;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_code || !seen_msg) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── INVOKE ─────────────────────────────────────────────────── */

yvr_status_t yvr_invoke_encode(const yvr_invoke_t *r,
                               uint8_t            *buf,
                               size_t              cap,
                               size_t             *out_len)
{
    if (r == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (r->tool_hash == NULL || r->tool_hash_len == 0) return YVR_E_INVALID_ARG;
    if (r->method == NULL || r->method_len == 0)       return YVR_E_INVALID_ARG;
    if (r->args == NULL && r->args_len != 0)           return YVR_E_INVALID_ARG;
    if (r->approval == NULL && r->approval_len != 0)   return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    /* CTAP2 order: v < args < method < approval < tool_hash. */
    const bool has_approval = (r->approval != NULL && r->approval_len > 0);
    yvr_cbor_w_map_begin(&w, has_approval ? 5 : 4);

    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, r->protocol_version);

    yvr_cbor_w_text(&w, "args", 4);
    yvr_cbor_w_bytes(&w, r->args, r->args_len);

    yvr_cbor_w_text(&w, "method", 6);
    yvr_cbor_w_text(&w, r->method, r->method_len);

    if (has_approval) {
        yvr_cbor_w_text(&w, "approval", 8);
        yvr_cbor_w_bytes(&w, r->approval, r->approval_len);
    }

    yvr_cbor_w_text(&w, "tool_hash", 9);
    yvr_cbor_w_bytes(&w, r->tool_hash, r->tool_hash_len);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_invoke_decode(const uint8_t *buf,
                               size_t         n,
                               yvr_invoke_t  *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_invoke_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_args = false, seen_method = false, seen_hash = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "args", 4) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->args, &out->args_len);
            if (s != YVR_OK) return s;
            seen_args = true;
            continue;
        }
        if (kn == 6 && memcmp(k, "method", 6) == 0) {
            s = yvr_cbor_r_text(&r, &out->method, &out->method_len);
            if (s != YVR_OK) return s;
            seen_method = true;
            continue;
        }
        if (kn == 8 && memcmp(k, "approval", 8) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->approval, &out->approval_len);
            if (s != YVR_OK) return s;
            continue;
        }
        if (kn == 9 && memcmp(k, "tool_hash", 9) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->tool_hash, &out->tool_hash_len);
            if (s != YVR_OK) return s;
            seen_hash = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_args || !seen_method || !seen_hash) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── TOOL_RSP ───────────────────────────────────────────────── */

yvr_status_t yvr_tool_rsp_encode(const yvr_tool_rsp_t *r,
                                 uint8_t              *buf,
                                 size_t                cap,
                                 size_t               *out_len)
{
    if (r == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (r->tool_hash == NULL || r->tool_hash_len == 0) return YVR_E_INVALID_ARG;
    if (r->error == NULL && r->error_len != 0)         return YVR_E_INVALID_ARG;
    if (r->result == NULL && r->result_len != 0)       return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    /* Map size: v + result + status + tool_hash mandatory; error +
     * duration_ms optional. */
    const bool has_error    = (r->error != NULL && r->error_len > 0);
    const bool has_duration = (r->duration_ms != 0);
    size_t map_n = 4 + (has_error ? 1 : 0) + (has_duration ? 1 : 0);

    yvr_cbor_w_map_begin(&w, map_n);

    /* CTAP2 order: v < error < result < status < tool_hash <
     * duration_ms. */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, r->protocol_version);

    if (has_error) {
        yvr_cbor_w_text(&w, "error", 5);
        yvr_cbor_w_text(&w, r->error, r->error_len);
    }

    yvr_cbor_w_text(&w, "result", 6);
    yvr_cbor_w_bytes(&w, r->result, r->result_len);

    yvr_cbor_w_text(&w, "status", 6);
    yvr_cbor_w_int(&w, (int64_t)r->status);

    yvr_cbor_w_text(&w, "tool_hash", 9);
    yvr_cbor_w_bytes(&w, r->tool_hash, r->tool_hash_len);

    if (has_duration) {
        yvr_cbor_w_text(&w, "duration_ms", 11);
        yvr_cbor_w_uint(&w, (uint64_t)r->duration_ms);
    }

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_tool_rsp_decode(const uint8_t  *buf,
                                 size_t          n,
                                 yvr_tool_rsp_t *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_tool_rsp_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_result = false, seen_status = false, seen_hash = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 5 && memcmp(k, "error", 5) == 0) {
            s = yvr_cbor_r_text(&r, &out->error, &out->error_len);
            if (s != YVR_OK) return s;
            continue;
        }
        if (kn == 6 && memcmp(k, "result", 6) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->result, &out->result_len);
            if (s != YVR_OK) return s;
            seen_result = true;
            continue;
        }
        if (kn == 6 && memcmp(k, "status", 6) == 0) {
            int64_t v;
            s = yvr_cbor_r_int(&r, &v);
            if (s != YVR_OK) return s;
            if (v < INT32_MIN || v > INT32_MAX) return YVR_E_BAD_FRAME;
            out->status = (int32_t)v;
            seen_status = true;
            continue;
        }
        if (kn == 9 && memcmp(k, "tool_hash", 9) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->tool_hash, &out->tool_hash_len);
            if (s != YVR_OK) return s;
            seen_hash = true;
            continue;
        }
        if (kn == 11 && memcmp(k, "duration_ms", 11) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->duration_ms = (uint32_t)v;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_result || !seen_status || !seen_hash) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── STREAM_CHUNK ───────────────────────────────────────────── */

yvr_status_t yvr_stream_chunk_encode(const yvr_stream_chunk_t *c,
                                     uint8_t                  *buf,
                                     size_t                    cap,
                                     size_t                   *out_len)
{
    if (c == NULL || buf == NULL || out_len == NULL) {
        return YVR_E_INVALID_ARG;
    }
    if (c->data == NULL && c->data_len != 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);

    /* CTAP2 order: v < seq < data < stream_id < end_stream. */
    yvr_cbor_w_map_begin(&w, 5);

    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, c->protocol_version);

    yvr_cbor_w_text(&w, "seq", 3);
    yvr_cbor_w_uint(&w, (uint64_t)c->seq);

    yvr_cbor_w_text(&w, "data", 4);
    yvr_cbor_w_bytes(&w, c->data, c->data_len);

    yvr_cbor_w_text(&w, "stream_id", 9);
    yvr_cbor_w_uint(&w, (uint64_t)c->stream_id);

    yvr_cbor_w_text(&w, "end_stream", 10);
    yvr_cbor_w_bool(&w, c->end_stream);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_stream_chunk_decode(const uint8_t      *buf,
                                     size_t              n,
                                     yvr_stream_chunk_t *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_stream_chunk_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);

    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_seq = false, seen_data = false;
    bool seen_sid = false, seen_end = false;

    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 3 && memcmp(k, "seq", 3) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->seq = (uint32_t)v;
            seen_seq = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "data", 4) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->data, &out->data_len);
            if (s != YVR_OK) return s;
            seen_data = true;
            continue;
        }
        if (kn == 9 && memcmp(k, "stream_id", 9) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->stream_id = (uint32_t)v;
            seen_sid = true;
            continue;
        }
        if (kn == 10 && memcmp(k, "end_stream", 10) == 0) {
            s = yvr_cbor_r_bool(&r, &out->end_stream);
            if (s != YVR_OK) return s;
            seen_end = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_seq || !seen_data || !seen_sid || !seen_end) {
        return YVR_E_BAD_FRAME;
    }
    return YVR_OK;
}

/* ── NEED ───────────────────────────────────────────────────── */

yvr_status_t yvr_need_encode(const yvr_need_t *r,
                             uint8_t          *buf,
                             size_t            cap,
                             size_t           *out_len)
{
    if (r == NULL || buf == NULL || out_len == NULL) return YVR_E_INVALID_ARG;
    if (r->tool_hash == NULL || r->tool_hash_len == 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);
    yvr_cbor_w_map_begin(&w, 2);

    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, r->protocol_version);

    yvr_cbor_w_text(&w, "tool_hash", 9);
    yvr_cbor_w_bytes(&w, r->tool_hash, r->tool_hash_len);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_need_decode(const uint8_t *buf,
                             size_t         n,
                             yvr_need_t    *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_need_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);
    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_hash = false;
    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;
        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 9 && memcmp(k, "tool_hash", 9) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->tool_hash, &out->tool_hash_len);
            if (s != YVR_OK) return s;
            seen_hash = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_hash) return YVR_E_BAD_FRAME;
    return YVR_OK;
}

/* ── MODULE ─────────────────────────────────────────────────── */

yvr_status_t yvr_module_body_encode(const yvr_module_body_t *m,
                                    uint8_t                 *buf,
                                    size_t                   cap,
                                    size_t                  *out_len)
{
    if (m == NULL || buf == NULL || out_len == NULL) return YVR_E_INVALID_ARG;
    if (m->wasm == NULL || m->wasm_len == 0)             return YVR_E_INVALID_ARG;
    if (m->descriptor == NULL || m->descriptor_len == 0) return YVR_E_INVALID_ARG;

    yvr_cbor_w_t w;
    yvr_cbor_w_init(&w, buf, cap);
    yvr_cbor_w_map_begin(&w, 3);

    /* CTAP2 order: "v" (0x61) < "wasm" (0x64) < "descriptor" (0x6a). */
    yvr_cbor_w_text(&w, "v", 1);
    w_protocol_version(&w, m->protocol_version);

    yvr_cbor_w_text(&w, "wasm", 4);
    yvr_cbor_w_bytes(&w, m->wasm, m->wasm_len);

    yvr_cbor_w_text(&w, "descriptor", 10);
    yvr_cbor_w_bytes(&w, m->descriptor, m->descriptor_len);

    yvr_status_t s = yvr_cbor_w_status(&w);
    if (s != YVR_OK) return s;
    *out_len = yvr_cbor_w_len(&w);
    return YVR_OK;
}

yvr_status_t yvr_module_body_decode(const uint8_t     *buf,
                                    size_t             n,
                                    yvr_module_body_t *out)
{
    if (buf == NULL || out == NULL) return YVR_E_INVALID_ARG;
    *out = (yvr_module_body_t){0};

    yvr_cbor_r_t r;
    yvr_cbor_r_init(&r, buf, n);
    size_t kv;
    yvr_status_t s = yvr_cbor_r_map_begin(&r, &kv);
    if (s != YVR_OK) return s;

    bool seen_v = false, seen_wasm = false, seen_desc = false;
    for (size_t i = 0; i < kv; i++) {
        const char *k;
        size_t      kn;
        s = yvr_cbor_r_text(&r, &k, &kn);
        if (s != YVR_OK) return s;

        if (kn == 1 && memcmp(k, "v", 1) == 0) {
            uint64_t v;
            s = yvr_cbor_r_uint(&r, &v);
            if (s != YVR_OK) return s;
            if (v > 0xFFFFFFFFu) return YVR_E_BAD_FRAME;
            out->protocol_version = (uint32_t)v;
            seen_v = true;
            continue;
        }
        if (kn == 4 && memcmp(k, "wasm", 4) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->wasm, &out->wasm_len);
            if (s != YVR_OK) return s;
            seen_wasm = true;
            continue;
        }
        if (kn == 10 && memcmp(k, "descriptor", 10) == 0) {
            s = yvr_cbor_r_bytes(&r, &out->descriptor, &out->descriptor_len);
            if (s != YVR_OK) return s;
            seen_desc = true;
            continue;
        }
        s = yvr_cbor_r_skip(&r);
        if (s != YVR_OK) return s;
    }
    if (!seen_v || !seen_wasm || !seen_desc) return YVR_E_BAD_FRAME;
    return YVR_OK;
}
