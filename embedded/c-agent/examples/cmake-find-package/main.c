/* Minimal vendor consumer — exercises both the c-agent core (frame
 * codec) and the host library (module supervisor). If this builds
 * + runs against an installed prefix, your CMake integration is
 * correct end-to-end. */

#include <stdio.h>
#include <yvr/frame.h>
#include <yvr/host.h>
#include <yvr/status.h>

int main(void)
{
    /* core: round-trip a HELLO frame header. */
    yvr_frame_header_t in = {
        .length = 17, .type = (uint8_t)YVR_FRAME_HELLO,
        .flags = 0,   .stream_id = 0,
    };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_OK) return 1;
    yvr_frame_header_t out;
    if (yvr_frame_header_decode(buf, sizeof(buf), &out) != YVR_OK) return 2;

    /* host: spin up + tear down. The stub backend means control
     * APIs return NOT_READY, but init/shutdown work and exercise
     * the linker path. */
    yvr_host_t *h = yvr_host_init("/tmp/yvr-example-state");
    if (h == NULL) {
        fprintf(stderr, "yvr_host_init failed\n");
        return 3;
    }
    yvr_host_shutdown(h);

    printf("ok: frame round-trip OK, host init/shutdown OK\n");
    return 0;
}
