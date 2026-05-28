# Managed Cloud Companion Declaration

Yaver mobile is a free companion app for Yaver developer machines. It can
connect to self-hosted machines or to a Yaver managed cloud machine that the
user already has on their account.

Store-build rules:

- The mobile app does not sell managed cloud.
- The mobile app does not create, display, or open external checkout URLs.
- The mobile app does not show prices, top-up controls, or purchase CTAs.
- Managed cloud checkout, credits, invoices, and cancellation live outside the
  mobile app.
- The mobile app only consumes existing entitlements: machine status, lifecycle
  controls, connection, Hermes builds, and runner credential setup.

Apple review positioning: free stand-alone companion to a paid web-based
web-hosting/cloud-development service under App Review Guideline 3.1.3(f).

Google Play positioning: consumption-only companion. Users may sign in and use
managed cloud machines acquired elsewhere, but cannot purchase access to the
cloud service from inside the Play-distributed app.

Yaver remains usable without managed cloud through self-hosted local or remote
developer machines running `yaver serve`.
