// HN-LAUNCH-HIDE-PAID: temporarily hide managed-cloud / billing / pricing /
// "buy a box" surfaces so the mobile app reads as pure free + open-source +
// self-hosted for the HN launch. Flip HIDE_PAID_UI to `false` to restore every
// Yaver-billed managed-cloud entry point. (grep this token to find every gated
// surface across web + mobile.)
//
// SCOPE: this flag hides only the MANAGED (Yaver-billed) buy/checkout/credit /
// "Yaver Cloud" purchase surfaces and their nav entry points. It must NOT hide
// BYO / self-host functionality — connecting your own machines, BYO Hetzner-
// token provisioning, self-hosted relay config, claiming your own devices.
// Those are the free self-hosted story and stay visible.
//
// Mirrors the identical `HIDE_PAID_UI` flag in web/app/page.tsx.
export const HIDE_PAID_UI = true;
