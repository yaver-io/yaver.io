/* Same source as cmake-find-package/main.c — proves both
 * consumption paths terminate at the same library. */

#include <stdio.h>
#include <yvr/frame.h>
#include <yvr/host.h>
#include <yvr/status.h>

int main(void)
{
    yvr_frame_header_t in = {
        .length = 17, .type = (uint8_t)YVR_FRAME_HELLO,
        .flags = 0,   .stream_id = 0,
    };
    uint8_t buf[YVR_FRAME_HEADER_SIZE];
    if (yvr_frame_header_encode(&in, buf, sizeof(buf)) != YVR_OK) return 1;
    yvr_frame_header_t out;
    if (yvr_frame_header_decode(buf, sizeof(buf), &out) != YVR_OK) return 2;

    yvr_host_t *h = yvr_host_init("/tmp/yvr-example-state");
    if (h == NULL) return 3;
    yvr_host_shutdown(h);

    printf("ok: frame + host linked via pkg-config\n");
    return 0;
}
