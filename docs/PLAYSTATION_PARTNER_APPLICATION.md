# Yaver PlayStation partner application packet

Status: submitted 2026-09-26 — awaiting Sony review; no PlayStation support
claim. The submission used the Türkiye **Tools & Middleware** route and included
one combined proposal covering Yaver and Talos. The application identifier is
kept out of this public repository.

Live portal check: 2026-09-26. For a company based in Türkiye, the public
registration flow currently offers **Tools & Middleware** as the closest honest
partnership type for Yaver. The prerequisite page asks for legal-business proof,
a named private-domain email address, and a product pitch/GDD or planned-project
sheet. It also asks applicants to include game-title information. Yaver has no
game title, so the application must say that explicitly and ask Sony to assess
it as a development-workflow tool. Do not invent a game or select a game
publisher category merely to advance the form.

Yaver must not use unofficial SDKs, leaked documentation, jailbroken retail
consoles, or second-hand development hardware. A Sony-authorized company
representative submits this packet through PlayStation Partners. Do not paste
private keys, infrastructure addresses, customer data, or unpublished Sony
material into this repository or the application.

## Submission order

1. Create or use a company-controlled PlayStation account with MFA.
2. Register the legal company at <https://partners.playstation.net/> and select
   **Tools & Middleware**. The current public form has no general non-game app
   category.
3. State in the first paragraph that Yaver is a **non-game development-workflow
   tool**, has no game title, and needs an eligibility decision before any
   PlayStation-specific implementation begins.
4. Submit the project summary below without pretending it is a game.
5. Ask for written confirmation that the category, existing-account model,
   private-relay networking, and free Store listing are acceptable.
6. If approved, have an authorized representative review/sign the GDPA.
7. Obtain the official SDK, certification rules, title IDs, and publishing
   documentation from the partner portal.
8. Request the complimentary development and test kits if Yaver is eligible.
9. Record only non-confidential decisions in this public repository. Keep Sony
   SDK code and documentation in the access-controlled environment required by
   the agreement.

## Project plan text

### Application opening

Yaver is applying under Tools & Middleware as a non-game development-workflow
tool. It is not a game and there is no associated game title. We are seeking
Sony's confirmation that the proposed workflow is eligible for this partner
category and, separately, whether a controller-first companion client may be
distributed on PlayStation. We will not begin PlayStation-specific SDK work or
claim platform support until Sony grants access and confirms the appropriate
technical and publishing routes.

### Product

**Yaver — secure remote development companion**

Yaver is an open-source remote-development product. Coding agents and source
repositories remain on a computer or server chosen by the user. The proposed
PlayStation application is a controller and review surface: it does not compile
code, execute downloaded programs, expose a shell, or modify the console.

For registered PlayStation developers, the planned tools project would connect
to an authorized development workstation and present bounded build, test, and
review workflows. Any integration with PlayStation SDK commands, logs, test
hardware, or certification tooling would be implemented only after approval,
inside Sony's required access-controlled environment. Confidential Sony SDK
material would never be copied into Yaver's public repository or relayed to an
unauthorized device.

### Console experience

- Pair through a short-lived device code approved on a trusted phone/computer.
- Select an authorized development machine and project.
- Start or continue a coding task.
- Read structured progress and results at television distance.
- Approve, reject, or cancel bounded actions.
- View application previews streamed from the user's development machine.
- Recover honestly from offline, expired-session, and unavailable-runtime
  states without exposing credentials.

### Audience and value

Software developers who want to monitor and review long-running development
tasks away from their desk. The product is controller-first and complements,
rather than replaces, the workstation hosting the development runtime.

### Network and data model

The client uses TLS to Yaver's identity/control service and to a user-selected
private or managed relay. Authorization is independently enforced by the target
runtime. Source repositories and arbitrary files are not downloaded to the
console. Tokens use secure platform storage and can be remotely revoked.

### Commercial model

The intended console client is free to download. Initial submission should not
include purchases or external checkout links. Existing Yaver accounts may sign
in, subject to Sony's written policy guidance.

### Schedule

Development begins only after product-category approval and official SDK
access. Release timing remains `TBD after approval`; do not promise a date that
precedes certification evidence on official hardware.

### Game-title disclosure

There is no game title associated with this application. Yaver is the planned
tools project. If Sony requires sponsorship by, or deployment with, a specific
registered game title before reviewing a Tools & Middleware application, ask
Sony to pause or redirect the application; do not fabricate title information.

## Talos eligibility boundary

Talos was included as a distinct secondary proposed product at the applicant's
request. The submitted proposal describes it truthfully as an existing ERP and
company-operations product, not a game engine or existing PlayStation
middleware. It asks Sony to make an independent eligibility/routing decision
for Talos and to identify a separate non-game/business channel if Tools &
Middleware is not appropriate. Do not claim PlayStation support for Talos
unless Sony names a route and the product passes that route's technical and
publishing requirements.

## Questions requiring Sony's written answer

1. Does Sony accept a non-game remote-development controller in the
   PlayStation Store?
2. Which program and application category should be used?
3. May a free client authenticate existing Yaver accounts without commerce?
4. Are outbound TLS, SSE/WebSocket, and approved realtime-preview transports
   permitted for this use case?
5. What restrictions apply to user-generated task text and streamed previews?
6. Is Yaver eligible for complimentary PS5 development and test kits?
7. Must reusable open-source code remain outside the confidential SDK module,
   and what repository controls does Sony require?

Any unclear answer is a release blocker, not permission to infer approval.
