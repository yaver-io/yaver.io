# Machine Profile

summary: Primary machine profile for Yaver mesh planning.
tags: mac, linux, docker, ssd
signatures: testflight, android, playstore, local-llm
preferred_for: planning, coding, ios, android, deploy

Use this file per machine to help Yaver choose where work should run.

Examples:
- A Mac Mini used for Xcode/TestFlight:
  tags: mac, xcode, ssd
  signatures: ios, testflight
  preferred_for: planning, ios, deploy

- A Linux/Hetzner box used for Android/builds:
  tags: linux, hetzner, docker, ssd
  signatures: android, playstore, gradle
  preferred_for: coding, android, deploy

- A local Ollama host:
  tags: linux, ollama, local-llm
  signatures: ollama
  preferred_for: coding, local-llm
