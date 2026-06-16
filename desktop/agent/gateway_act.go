package main

// gateway_act.go — the ACT (write) execution wrapper (M-G7).
//
// gatewayInvoke (gateway_invoke.go) READS state as you. gatewayActExecute WRITES
// state as you — it starts a charge, buys a ticket, places an order, pays an
// invoice. Acting as the user is the highest-risk thing the gateway does, so an
// act NEVER runs as a bare call. It goes through a fixed safety pipeline (docs
// §16):
//
//	1. Policy Guard  — EvaluateAccessPolicy(source, action, jurisdiction). A
//	   "block" stops here (a block is a "no"); a "warn" forces a confirm.
//	2. Velocity cap  — at most N executed acts/connector/hour (from the audit
//	   ledger), so a runaway loop can't drain an account.
//	3. Dry-run       — buildActPreview describes EXACTLY what will be sent
//	   (method+endpoint or the redroid step list) WITHOUT mutating anything.
//	4. Two-key confirm — the human gate (gateway_gate.go) asks the user on THEIR
//	   OWN phone. A low, reversible act may be confirmed by voice ("yes"); a high
//	   or financial act requires a TAPPED approval on a second surface — never
//	   voice-alone (docs §16: "irreversible + financial over threshold needs a
//	   tapped confirm on a second surface").
//	5. Execute       — with an Idempotency-Key so a retried act isn't doubled.
//	   A 403/429/451 from the service is a block: stop, never rotate/retry-spam.
//	6. Audit         — append one line to the LOCAL ledger (never Convex).
//
// The machine never satisfies a human factor itself (no captcha solve, no 2FA
// bypass) — those route through the same human gate as reads do.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// gatewayActMaxPerHour bounds executed acts per connector per hour (velocity
// cap). Conservative default; a per-connector override can lower it later.
var gatewayActMaxPerHour = 20

// ActPreview is the dry-run description of an act — what WOULD happen, with no
// mutation performed. Returned by gateway_act{dry_run:true} for the user/host to
// review before confirming.
type ActPreview struct {
	Connector      string         `json:"connector"`
	Capability     string         `json:"capability"`
	Verb           string         `json:"verb"`
	Risk           string         `json:"risk"`
	Engine         string         `json:"engine"`
	Method         string         `json:"method,omitempty"`   // api
	Endpoint       string         `json:"endpoint,omitempty"` // api (params substituted)
	BodyPreview    string         `json:"bodyPreview,omitempty"`
	Steps          []string       `json:"steps,omitempty"` // redroid (human-readable)
	Policy         PolicyDecision `json:"policy"`
	RequiresTapKey bool           `json:"requiresTapKey"` // true ⇒ must tap a 2nd surface, voice-alone refused
	ConfirmPrompt  string         `json:"confirmPrompt"`
	Idempotency    string         `json:"idempotency"`
	Summary        string         `json:"summary"` // one line for TTS / a notification
}

// ActResult is the outcome of an executed (or refused) act.
type ActResult struct {
	Connector  string                 `json:"connector"`
	Capability string                 `json:"capability"`
	Verb       string                 `json:"verb"`
	Outcome    string                 `json:"outcome"` // executed|declined|blocked_policy|blocked_rate|blocked_remote|error
	StatusCode int                    `json:"statusCode,omitempty"`
	Answer     map[string]interface{} `json:"answer,omitempty"`
	Confirmed  string                 `json:"confirmed,omitempty"`
	Detail     string                 `json:"detail,omitempty"`
	AuditID    string                 `json:"auditId,omitempty"`
	Summary    string                 `json:"summary"`
}

// actExecOptions tunes how the act is confirmed and audited.
type actExecOptions struct {
	// PreApproved skips the live human gate — used by the explicit MCP
	// dry-run→confirm round trip where the separate confirm call IS the second
	// key. NEVER set this from a single voice utterance for a high/financial act.
	PreApproved bool
	// VoiceAnswer carries an inline spoken confirmation ("yes") for the
	// voice/router path. Honored ONLY for low-risk acts (a tapped key is required
	// for high/financial); ignored otherwise.
	VoiceAnswer string
	// Gate is the human-gate store to block on when a live confirm is needed.
	// nil ⇒ the process-wide gatewayGates (notifies the user's phone).
	Gate *gateStore
	// Jurisdiction feeds the Policy Guard ("TR"/"US"/…); "" = unknown.
	Jurisdiction string
}

// confirmPlanFor returns how an act of the given risk tier must be confirmed.
func confirmPlanFor(tier riskTier) (kind GateKind, tapRequired bool) {
	switch tier {
	case riskFinancial, riskHigh:
		return GateApprovePush, true // tapped second key, never voice-alone
	default: // riskLow / anything else acting → confirm, voice "yes" acceptable
		return GateSimpleConfirm, false
	}
}

// actActionWord maps an act onto a Policy-Guard action word so funding verbs
// (deposit/withdraw/pay…) get the jurisdiction check, while ordinary acts read
// as generic (allowed unless a rule blocks the source).
func actActionWord(cap *Capability) string {
	v := strings.ToLower(strings.TrimSpace(cap.Verb))
	switch v {
	case "pay", "deposit", "withdraw", "fund", "bet", "place_bet", "stake":
		return v
	}
	if gatewayRiskTier(cap.Risk) == riskFinancial {
		return "fund"
	}
	return v
}

// actIdempotencyKey derives a stable key for (connector, capability, params) on
// the current day, so an accidental double-submit within the day is dedupable by
// the service. It is a hash — safe to record in the audit ledger.
func actIdempotencyKey(connectorID, capabilityID string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", connectorID, capabilityID, time.Now().UTC().Format("2006-01-02"))
	for _, k := range keys {
		fmt.Fprintf(h, "\x00%s=%s", k, params[k])
	}
	return "yvract_" + hex.EncodeToString(h.Sum(nil)[:12])
}

// buildActPreview describes an act WITHOUT performing it. Pure + offline.
func buildActPreview(conn *Connector, cap *Capability, params map[string]string, jurisdiction string) *ActPreview {
	tier := gatewayRiskTier(cap.Risk)
	_, tapRequired := confirmPlanFor(tier)
	policy := EvaluateAccessPolicy(conn.Surface, actActionWord(cap), jurisdiction)

	p := &ActPreview{
		Connector:      conn.ID,
		Capability:     cap.ID,
		Verb:           cap.Verb,
		Risk:           cap.Risk,
		Engine:         conn.Engine,
		Policy:         policy,
		RequiresTapKey: tapRequired,
		Idempotency:    actIdempotencyKey(conn.ID, cap.ID, params),
	}
	switch conn.Engine {
	case "api":
		p.Method = strings.ToUpper(strings.TrimSpace(cap.Flow.Method))
		p.Endpoint = gatewayJoinURL(conn.Surface, substituteParams(cap.Flow.Path, params))
		if strings.TrimSpace(cap.Flow.Body) != "" {
			p.BodyPreview = substituteParams(cap.Flow.Body, params)
		}
	case "redroid":
		pkg := gatewayFirstNonEmpty(cap.Flow.LaunchPkg, conn.Surface)
		p.Steps = append(p.Steps, "launch "+pkg)
		for _, st := range cap.Flow.Steps {
			p.Steps = append(p.Steps, actStepLabel(st, params))
		}
	}
	title := strings.TrimSpace(cap.Title)
	if title == "" {
		title = cap.ID
	}
	p.Summary = fmt.Sprintf("%s on %s (%s)", title, conn.ID, cap.Risk)
	if tapRequired {
		p.ConfirmPrompt = fmt.Sprintf("Approve on your phone: %s. (%s — tap to confirm.)", p.Summary, cap.Risk)
	} else {
		p.ConfirmPrompt = fmt.Sprintf("Confirm: %s?", p.Summary)
	}
	return p
}

// actStepLabel renders a redroid step for the human-readable preview (no secrets
// — the typed text is shown as substituted, which for an act is item/quantity
// data the user supplied, not a credential).
func actStepLabel(st FlowStep, params map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(st.Action)) {
	case "tap", "click":
		return "tap " + st.Target
	case "type", "input":
		return "type " + substituteParams(st.Text, params) + " → " + st.Target
	case "wait", "":
		return "wait"
	}
	return st.Action
}

// gatewayActExecute runs the full ACT safety pipeline for one capability and
// returns the outcome. It always appends exactly one audit line.
func (d *gatewayDeps) gatewayActExecute(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, opts actExecOptions) (*ActResult, error) {
	if isReadVerb(cap.Verb) {
		return nil, fmt.Errorf("gateway: capability %q is a read — use gateway_query, not gateway_act", cap.ID)
	}
	tier := gatewayRiskTier(cap.Verb)
	_ = tier
	preview := buildActPreview(conn, cap, params, opts.Jurisdiction)
	res := &ActResult{Connector: conn.ID, Capability: cap.ID, Verb: cap.Verb, Summary: preview.Summary}

	audit := GatewayAuditEntry{
		Connector:   conn.ID,
		Capability:  cap.ID,
		Verb:        cap.Verb,
		Risk:        cap.Risk,
		Engine:      conn.Engine,
		Target:      actTarget(conn, cap, preview),
		Decision:    preview.Policy.Decision,
		Idempotency: preview.Idempotency,
	}
	finish := func(outcome, detail string, code int, confirmed string) (*ActResult, error) {
		res.Outcome = outcome
		res.Detail = detail
		res.StatusCode = code
		res.Confirmed = confirmed
		audit.Outcome = outcome
		audit.Detail = gatewayTruncate(detail, 300)
		audit.StatusCode = code
		audit.Confirmed = confirmed
		audit.ID = preview.Idempotency
		_ = appendGatewayAudit(audit)
		res.AuditID = audit.ID
		return res, nil
	}

	// 1. Policy Guard — a block is a hard "no".
	if preview.Policy.Decision == "block" {
		return finish("blocked_policy", preview.Policy.Reason, 0, "")
	}

	// 2. Velocity cap — protect the account from a runaway loop.
	if n, err := gatewayActCountSince(conn.ID, time.Now().Add(-time.Hour)); err == nil && n >= gatewayActMaxPerHour {
		return finish("blocked_rate", fmt.Sprintf("velocity cap reached: %d acts on %q in the last hour (max %d)", n, conn.ID, gatewayActMaxPerHour), 0, "")
	}

	// 3+4. Confirm (unless the explicit MCP confirm round-trip pre-approved it).
	confirmed := "explicit"
	if !opts.PreApproved {
		kind, tapRequired := confirmPlanFor(gatewayRiskTier(cap.Risk))
		// A low-risk act may be confirmed inline by voice; high/financial may not.
		if !tapRequired && strings.TrimSpace(opts.VoiceAnswer) != "" {
			if !gatewayAnswerApproves(opts.VoiceAnswer) {
				return finish("declined", "voice confirmation was not affirmative", 0, "")
			}
			confirmed = "voice"
		} else {
			// Block on the human gate (notifies the user's phone). For a financial
			// act this is the mandatory tapped second key.
			gate := opts.Gate
			if gate == nil {
				gate = gatewayGates
			}
			resolution, err := gate.awaitHuman(ctx, GateRequest{
				ConnectorID: conn.ID,
				Kind:        kind,
				Prompt:      preview.ConfirmPrompt,
				Options:     []string{"approve", "deny"},
			})
			if err != nil {
				return finish("declined", "confirmation aborted: "+err.Error(), 0, "")
			}
			if resolution.Status != GateResolved || !resolution.Approved {
				return finish("declined", "user did not approve (status "+string(resolution.Status)+")", 0, "")
			}
			confirmed = "phone_tap"
		}
	}

	// 5. Execute.
	session, err := d.broker.Ensure(ctx, conn)
	if err != nil {
		return finish("error", "auth: "+err.Error(), 0, confirmed)
	}
	switch conn.Engine {
	case "api":
		return d.apiActExecute(ctx, conn, cap, params, session, preview, confirmed, finish, res)
	case "redroid":
		driver, ok := d.broker.deviceDriverFor(conn)
		if !ok || driver == nil {
			return finish("error", "redroid engine but no device driver available", 0, confirmed)
		}
		return d.redroidActExecute(ctx, conn, cap, params, driver, opts, confirmed, finish, res)
	default:
		return finish("error", "unsupported engine "+conn.Engine, 0, confirmed)
	}
}

// apiActExecute performs the mutating HTTP call (with one 401 refresh-retry) and
// records the outcome.
func (d *gatewayDeps) apiActExecute(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, session Session, preview *ActPreview, confirmed string, finish func(string, string, int, string) (*ActResult, error), res *ActResult) (*ActResult, error) {
	raw, status, err := d.apiActSend(ctx, conn, cap, params, session, preview.Idempotency)
	if err != nil {
		return finish("error", err.Error(), 0, confirmed)
	}
	if status == http.StatusUnauthorized {
		session2, rErr := d.broker.Refresh(ctx, conn)
		if rErr != nil {
			return finish("error", "401 and re-auth failed: "+rErr.Error(), status, confirmed)
		}
		raw, status, err = d.apiActSend(ctx, conn, cap, params, session2, preview.Idempotency)
		if err != nil {
			return finish("error", err.Error(), 0, confirmed)
		}
	}
	if status == http.StatusForbidden || status == http.StatusTooManyRequests || status == 451 {
		return finish("blocked_remote", fmt.Sprintf("service returned a block (status %d) — stopping, not retrying. A block is a \"no\".", status), status, confirmed)
	}
	if status < 200 || status >= 300 {
		return finish("error", fmt.Sprintf("status %d: %s", status, gatewayTruncate(string(raw), 256)), status, confirmed)
	}
	// Best-effort projection of the response (an act may return a receipt/id).
	if ans, perr := projectAnswer(raw, cap.AnswerSchema); perr == nil {
		res.Answer = ans
	}
	return finish("executed", "", status, confirmed)
}

// apiActSend builds and sends the mutating request.
func (d *gatewayDeps) apiActSend(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, session Session, idem string) ([]byte, int, error) {
	endpoint := gatewayJoinURL(conn.Surface, substituteParams(cap.Flow.Path, params))
	method := strings.ToUpper(strings.TrimSpace(cap.Flow.Method))
	var body io.Reader
	bodyStr := substituteParams(cap.Flow.Body, params)
	if strings.TrimSpace(bodyStr) != "" {
		body = strings.NewReader(bodyStr)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build act request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gatewayContactUA) // honest identity, never a spoof
	if strings.TrimSpace(bodyStr) != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	if session.Kind == SessionBearer && session.Token != "" {
		req.Header.Set("Authorization", "Bearer "+session.Token)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("act request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return raw, resp.StatusCode, nil
}

// redroidActExecute drives the act flow on the logged-in device. It reuses the
// read engine's primitives (observeScreen, runFlowStep, detectBlockSignal,
// gateChallenge) — an act on a phone app is the same tap/type sequence as a read,
// the difference is that a tap here commits a purchase. A block signal stops the
// flow; a challenge routes to the human gate.
func (d *gatewayDeps) redroidActExecute(ctx context.Context, conn *Connector, cap *Capability, params map[string]string, driver deviceDriver, opts actExecOptions, confirmed string, finish func(string, string, int, string) (*ActResult, error), res *ActResult) (*ActResult, error) {
	pkg := gatewayFirstNonEmpty(cap.Flow.LaunchPkg, conn.Surface)
	if err := driver.Launch(pkg); err != nil {
		return finish("error", "launch: "+err.Error(), 0, confirmed)
	}
	screen, err := observeScreen(driver, pkg)
	if err != nil {
		return finish("error", "observe: "+err.Error(), 0, confirmed)
	}
	for _, step := range cap.Flow.Steps {
		if block := detectBlockSignal(screen); block != "" {
			return finish("blocked_remote", "block detected on screen: "+block, 0, confirmed)
		}
		if reason := detectChallengeScreen(screen); reason != "" {
			if gErr := gateChallenge(ctx, conn, screen, reason); gErr != nil {
				return finish("declined", "challenge not resolved: "+gErr.Error(), 0, confirmed)
			}
		}
		if err := runFlowStep(driver, step, params); err != nil {
			return finish("error", "step: "+err.Error(), 0, confirmed)
		}
		if screen, err = observeScreen(driver, pkg); err != nil {
			return finish("error", "observe: "+err.Error(), 0, confirmed)
		}
	}
	if block := detectBlockSignal(screen); block != "" {
		return finish("blocked_remote", "block detected after flow: "+block, 0, confirmed)
	}
	if len(cap.AnswerSchema) > 0 {
		res.Answer = defaultExtractor.Extract(screen, cap.AnswerSchema)
	}
	return finish("executed", "screen "+screen.Signature, 0, confirmed)
}

// actTarget returns a redacted, human-readable description of what an act
// touches — for the audit ledger. NEVER a body value or a secret.
func actTarget(conn *Connector, cap *Capability, preview *ActPreview) string {
	switch conn.Engine {
	case "api":
		return strings.TrimSpace(preview.Method + " " + preview.Endpoint)
	case "redroid":
		return "redroid:" + gatewayFirstNonEmpty(cap.Flow.LaunchPkg, conn.Surface)
	}
	return conn.ID
}

// gatewayJoinURL joins a base surface and a path with exactly one slash.
func gatewayJoinURL(surface, path string) string {
	return strings.TrimRight(surface, "/") + "/" + strings.TrimLeft(path, "/")
}
