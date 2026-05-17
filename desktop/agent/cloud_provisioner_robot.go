package main

// cloud_provisioner_robot.go — Phase D0: the premium KVM SKU
// (Hetzner dedicated/bare-metal via the Robot webservice). This is
// the ONLY way to run the Android emulator for Flutter/Kotlin WebRTC
// — Hetzner Cloud (cx/cax, the api.hetzner.cloud path provisionHetzner
// uses) exposes no /dev/kvm on any plan; see memory
// project_no_linux_arm64_android_emulator + ..._sku_reality.
//
// FAIL-CLOSED BY DESIGN. Ordering a dedicated server is a paid,
// recurring (~€30-40+/mo), non-instant, hard-to-reverse commitment
// AND it uses a different API than the Cloud one (Robot webservice,
// HTTP basic auth, the server-market/auction for the only "fast"
// path). So this provisioner NEVER places an order unless ALL of:
//   1. HROBOT_USER + HROBOT_PASS are set (the user's Robot
//      webservice creds — a secret, env-only, never repo/Convex), and
//   2. opts["confirmPaidOrder"] == "true" (explicit per-call
//      acknowledgement that this spends money), and
//   3. opts["live"] == "true" (the order HTTP is otherwise a
//      dry-run that returns the plan only).
// Missing any → a Manual result that explains exactly what's needed
// and the cost. With env unset this is byte-for-byte a no-op, so
// shipping it changes nothing until the owner deliberately opts in.
//
// The order HTTP shape targets Hetzner's documented Robot endpoints
// (robot-ws.your-server.de, server_market) but the LIVE order path
// is intentionally a single guarded function the owner validates
// against their own Robot account before first real use — I will
// not pretend an unverified paid order path is proven.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const robotWebserviceBase = "https://robot-ws.your-server.de"

// hetznerRobotAPIBase is a test seam (var, not const) so the live
// order path is exercisable against an httptest fake Robot API —
// without a real account or a real (paid) order. Production keeps the
// real endpoint. The fake encodes my read of Hetzner's documented
// Robot webservice contract; the OWNER still validates against their
// real account before trusting it (a wrong call orders/charges real
// hardware), exactly like Phase 3/WDA's on-device step.
var hetznerRobotAPIBase = robotWebserviceBase

func provisionHetznerRobot(name string, opts map[string]string) (*ProvisionResult, error) {
	user := strings.TrimSpace(os.Getenv("HROBOT_USER"))
	pass := strings.TrimSpace(os.Getenv("HROBOT_PASS"))
	if user == "" || pass == "" {
		return &ProvisionResult{
			Provider: "hetzner-robot",
			Manual: "Premium KVM SKU (bare-metal, Flutter/Kotlin emulator) is not wired to an account. " +
				"Set HROBOT_USER + HROBOT_PASS (Hetzner Robot webservice creds — env/secret only, never the repo) " +
				"on the provisioning host, then retry with opts.confirmPaidOrder=true. " +
				"Bare-metal is a paid recurring order (~€30-40+/mo), different from instant Cloud boxes.",
		}, nil
	}
	if opts["confirmPaidOrder"] != "true" {
		return &ProvisionResult{
			Provider: "hetzner-robot",
			Manual: "Refusing to order paid bare-metal without explicit acknowledgement. " +
				"Ordering a Hetzner dedicated server is a recurring (~€30-40+/mo) non-instant commitment. " +
				"Re-call with opts.confirmPaidOrder=true (and opts.live=true to actually place the order; " +
				"otherwise a plan-only dry-run is returned).",
		}, nil
	}

	plan := opts["plan"]
	if plan == "" {
		plan = "kvm-emulator"
	}
	region := opts["region"]
	if region == "" {
		region = "eu"
	}
	// Plan: what a live order WOULD do (server-market/auction pick +
	// installimage Ubuntu + cloud-init equivalent + KVM verify).
	planResult := &ProvisionResult{
		OK:       true,
		Provider: "hetzner-robot",
		Resource: "bare-metal (dry-run)",
		Details: map[string]string{
			"plan":     plan,
			"region":   region,
			"name":     name,
			"endpoint": robotWebserviceBase + "/order/server_market",
			"mode":     "dry-run (opts.live != true) — no order placed, no charge",
		},
		Notes: "Would: query server_market for a KVM-capable bare-metal box, place a market order, " +
			"installimage Ubuntu, run the managed cloud-init, then verify /dev/kvm before marking ready. " +
			"Set opts.live=true to place the real (paid) order — validate this path against your Robot " +
			"account first; the live order function is deliberately separate and unproven in CI.",
	}
	if opts["live"] != "true" {
		return planResult, nil
	}
	// LIVE: place a real server-market (auction) order. Guards above
	// already required creds + confirmPaidOrder + live. This spends
	// money — it is reached ONLY with all three explicit.
	txID, productName, err := robotPlaceMarketOrder(user, pass, opts)
	if err != nil {
		return nil, fmt.Errorf("hetzner-robot order failed (no charge if this errored before the transaction POST): %w", err)
	}
	return &ProvisionResult{
		OK:       true,
		Provider: "hetzner-robot",
		Resource: "bare-metal",
		ID:       txID,
		Details: map[string]string{
			"transactionId": txID,
			"product":       productName,
			"plan":          plan,
			"region":        region,
			"name":          name,
		},
		Notes: "Server-market order placed (transaction " + txID + "). Bare-metal setup is async " +
			"(minutes): Hetzner provisions + runs installimage. Once SSH is up, run the managed " +
			"cloud-init/agent bootstrap and verify /dev/kvm before marking ready. Validate this " +
			"end-to-end against your own Robot account — the contract here is fake-server tested only.",
	}, nil
}

func robotBasicAuthGET(user, pass, path string, out interface{}) error {
	req, err := http.NewRequest("GET", hetznerRobotAPIBase+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("robot GET %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// robotPlaceMarketOrder queries the server-market (auction) for the
// cheapest product whose price is within opts.maxPriceEur (default
// 45) — that band is where KVM-capable dedicated boxes sit — then
// places the order. Returns (transactionId, productName). Implements
// Hetzner's documented Robot webservice contract; fake-server tested.
func robotPlaceMarketOrder(user, pass string, opts map[string]string) (string, string, error) {
	var products []struct {
		Product struct {
			ID    json.Number `json:"id"`
			Name  string      `json:"name"`
			Price struct {
				Recurring string `json:"recurring"`
			} `json:"price"`
		} `json:"product"`
	}
	if err := robotBasicAuthGET(user, pass, "/order/server_market/product", &products); err != nil {
		return "", "", fmt.Errorf("list server_market: %w", err)
	}
	if len(products) == 0 {
		return "", "", fmt.Errorf("server_market empty — no auction box available right now")
	}
	maxEur := 45.0
	if v := strings.TrimSpace(opts["maxPriceEur"]); v != "" {
		if f, perr := parseFloatLoose(v); perr == nil && f > 0 {
			maxEur = f
		}
	}
	pick := ""
	pickName := ""
	best := 1e18
	for _, p := range products {
		price, perr := parseFloatLoose(p.Product.Price.Recurring)
		if perr != nil || price <= 0 || price > maxEur {
			continue
		}
		if price < best {
			best = price
			pick = p.Product.ID.String()
			pickName = p.Product.Name
		}
	}
	if pick == "" {
		return "", "", fmt.Errorf("no server_market product within €%.0f/mo (KVM band) — raise opts.maxPriceEur or retry later", maxEur)
	}
	if want := strings.TrimSpace(opts["productId"]); want != "" {
		pick = want // explicit override wins
	}

	form := url.Values{}
	form.Set("product_id", pick)
	form.Set("dist", strings.TrimSpace(opts["dist"]))
	if form.Get("dist") == "" {
		form.Set("dist", "Ubuntu 24.04.3 LTS minimal")
	}
	if loc := strings.TrimSpace(opts["location"]); loc != "" {
		form.Set("location", loc)
	}
	if ak := strings.TrimSpace(opts["authorizedKey"]); ak != "" {
		form.Set("authorized_key[]", ak)
	}
	req, err := http.NewRequest("POST", hetznerRobotAPIBase+"/order/server_market/transaction", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("place order: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("server_market transaction HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tx struct {
		Transaction struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"transaction"`
	}
	if err := json.Unmarshal(body, &tx); err != nil {
		return "", "", fmt.Errorf("parse transaction: %w", err)
	}
	if tx.Transaction.ID == "" {
		return "", "", fmt.Errorf("server_market transaction returned no id")
	}
	return tx.Transaction.ID, pickName, nil
}

func parseFloatLoose(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}
