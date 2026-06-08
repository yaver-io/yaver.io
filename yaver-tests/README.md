# yaver-tests — the local app-test corpus

Git-versioned, `$0`, no telemetry. These run on **your** redroid (owner box free;
farm metered) via the Yaver app-test agent. Two kinds of test, one vocabulary:

```
yaver-tests/
  specs/   *.test.yaml   deterministic regression (testkit) — known steps + assertions
  flows/   *.flow.yaml   agentic exploration — a goal + expectations the LLM drives toward
  yaver-qa.yaml          suite defaults (documentation today; pass flags to qa_run)
```

A **spec** is a flow driven by a scripted brain; a **flow** is the same loop
driven by the LLM brain + the oracle bank (red-box / crash / ANR / blank-screen /
expectation). Design: `docs/yaver-ai-app-test-agent.md`.

## Run

```bash
# build a warm base once (skips cold boot + Yaver install every run)
yaver studio base build --yaver-apk ./app-release.apk     # → ~/.yaver/base/<arch>/<ver>

# deterministic specs (testkit) on redroid
#   target: android-redroid in the spec; set redroid.base to attach to the warm base

# agentic flows (catch-only) via the ops verb (MCP / mobile / web / CLI):
#   ops qa_run { "package":"io.yaver.mobile", "base":"<ver>", "flowsDir":"yaver-tests/flows" }
#   → returns a jobId; poll studio_job_status, then qa_report <jobId> for the report card
```

`base` restores the warm Yaver Base Image instead of cold-booting — the fast
path. Bugs are reported, never auto-committed; fix mode (catch→patch→reload→
re-verify) is a later phase and leaves a draft diff for review.
