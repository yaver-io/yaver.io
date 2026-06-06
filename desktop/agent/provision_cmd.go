package main

// provision_cmd.go — `yaver provision …`: the builder/factory + buyer CLI
// for zero-touch (DPP-style) device provisioning. This is the thin v1
// surface that third parties (Talos et al.) and solo builders use to mint
// claimable hardware identities and that a buyer can use to claim a box
// from a terminal. See provision.go (agent runtime) and
// backend/convex/provisioning.ts (server) for the full model.
//
// Verbs:
//   yaver provision mint   — generate a per-device seed, register it with
//                            Convex, write the SD seed file, print the QR
//   yaver provision qr     — re-print the QR for an existing seed file
//   yaver provision claim  — claim a device by scanned QR (buyer side)
//   yaver provision product — register a product/SKU (display name in UI)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
)

func runProvision(args []string) {
	if len(args) == 0 {
		printProvisionUsage()
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "mint":
		runProvisionMint(rest)
	case "qr":
		runProvisionQR(rest)
	case "claim":
		runProvisionClaim(rest)
	case "product":
		runProvisionProduct(rest)
	case "help", "-h", "--help":
		printProvisionUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown provision subcommand: %s\n\n", sub)
		printProvisionUsage()
		os.Exit(1)
	}
}

func printProvisionUsage() {
	fmt.Println(`yaver provision — zero-touch device provisioning

  yaver provision mint [flags]      Mint a device identity (factory/flash side)
      --product <slug>              Product/SKU this device belongs to
      --model <name>                Human model name baked into the QR
      --platform <os>               Target platform (default: linux)
      --name <name>                 Default device name
      --out <path>                  Where to write the SD seed (default: ./provision-<id>.json)
      --no-register                 Don't register with Convex (offline; you must mint later)

  yaver provision qr <seed.json>    Re-print the scannable QR for a seed

  yaver provision claim <qr|deviceId> [flags]   Claim a device (buyer side)
      --secret <s>                  Claim secret (when passing a bare deviceId)
      --name <name>                 Name the device on claim

  yaver provision product --id <slug> --name <name> [--vendor <v>]
                                    Register a product/SKU display name`)
}

// provisionAuth returns the local config's convex URL + bearer token, or
// exits with a friendly message when the box isn't signed in.
func provisionAuth() (convexURL, token string) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run `yaver auth` first (the builder/owner account).")
		os.Exit(1)
	}
	convexURL = strings.TrimRight(cfg.ConvexSiteURL, "/")
	if convexURL == "" {
		convexURL = strings.TrimRight(defaultConvexSiteURL, "/")
	}
	return convexURL, cfg.AuthToken
}

func runProvisionMint(args []string) {
	var productID, model, platform, name, out string
	platform = "linux"
	register := true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--product":
			i++
			if i < len(args) {
				productID = args[i]
			}
		case "--model":
			i++
			if i < len(args) {
				model = args[i]
			}
		case "--platform":
			i++
			if i < len(args) {
				platform = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				name = args[i]
			}
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		case "--no-register":
			register = false
		}
	}

	convexURL, token := "", ""
	if register {
		convexURL, token = provisionAuth()
	} else {
		// Offline mint still needs a convex URL baked into the seed so the
		// box knows where to attest. Use config's if available.
		if cfg, err := LoadConfig(); err == nil && cfg != nil {
			convexURL = strings.TrimRight(cfg.ConvexSiteURL, "/")
		}
		if convexURL == "" {
			convexURL = strings.TrimRight(defaultConvexSiteURL, "/")
		}
	}

	seed, err := GenerateProvisionSeed(productID, model, platform, convexURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint: %v\n", err)
		os.Exit(1)
	}

	if register {
		pub, err := seed.PublicKeyBase64()
		if err != nil {
			fmt.Fprintf(os.Stderr, "mint: %v\n", err)
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]interface{}{
			"deviceId":        seed.DeviceID,
			"publicKey":       pub,
			"claimSecretHash": claimSecretHashHex(seed.ClaimSecret),
			"productId":       productID,
			"name":            name,
			"platform":        platform,
		})
		if err := provisionPost(convexURL+"/devices/provision-mint", token, body); err != nil {
			fmt.Fprintf(os.Stderr, "mint: register with Convex failed: %v\n", err)
			os.Exit(1)
		}
	}

	if out == "" {
		out = fmt.Sprintf("./provision-%s.json", seed.DeviceID)
	}
	if err := writeProvisionSeed(out, seed); err != nil {
		fmt.Fprintf(os.Stderr, "mint: write seed: %v\n", err)
		os.Exit(1)
	}

	uri, err := seed.ProvisionQRURI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint: build QR: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("✓ Minted device %s%s\n", seed.DeviceID, regSuffix(register))
	fmt.Printf("  Seed (flash to SD boot partition as 'yaver-provision.json'): %s\n", out)
	fmt.Println()
	fmt.Println("  Print this QR on the device label. The buyer scans it in the")
	fmt.Println("  Yaver app to take ownership — even before powering the box on:")
	fmt.Println()
	printProvisionQR(uri)
	fmt.Println()
	fmt.Printf("  QR payload: %s\n", uri)
	fmt.Println()
}

func regSuffix(registered bool) string {
	if registered {
		return " (registered with Convex)"
	}
	return " (NOT registered — run mint online before shipping)"
}

func runProvisionQR(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver provision qr <seed.json>")
		os.Exit(1)
	}
	seed, err := readProvisionSeed(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "qr: %v\n", err)
		os.Exit(1)
	}
	uri, err := seed.ProvisionQRURI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qr: %v\n", err)
		os.Exit(1)
	}
	printProvisionQR(uri)
	fmt.Printf("\n  %s\n", uri)
}

func runProvisionClaim(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver provision claim <qr-uri|deviceId> [--secret <s>] [--name <n>]")
		os.Exit(1)
	}
	target := args[0]
	var secret, name string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--secret":
			i++
			if i < len(args) {
				secret = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				name = args[i]
			}
		}
	}

	deviceID := target
	convexOverride := ""
	if strings.HasPrefix(strings.TrimSpace(target), "yaver://provision") {
		claim, err := ParseProvisionQR(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "claim: %v\n", err)
			os.Exit(1)
		}
		deviceID = claim.DeviceID
		if secret == "" {
			secret = claim.ClaimSecret
		}
		convexOverride = claim.ConvexSiteURL
	}
	if secret == "" {
		fmt.Fprintln(os.Stderr, "claim: a claim secret is required (pass a full QR URI or --secret)")
		os.Exit(1)
	}

	convexURL, token := provisionAuth()
	if convexOverride != "" {
		convexURL = strings.TrimRight(convexOverride, "/")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"deviceId":    deviceID,
		"claimSecret": secret,
		"name":        name,
	})
	if err := provisionPost(convexURL+"/devices/provision-claim", token, body); err != nil {
		fmt.Fprintf(os.Stderr, "claim: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Claimed device %s — it will self-credential to your account on next boot.\n", deviceID)
}

func runProvisionProduct(args []string) {
	var id, name, vendor string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			i++
			if i < len(args) {
				id = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				name = args[i]
			}
		case "--vendor":
			i++
			if i < len(args) {
				vendor = args[i]
			}
		}
	}
	if id == "" || name == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver provision product --id <slug> --name <name> [--vendor <v>]")
		os.Exit(1)
	}
	convexURL, token := provisionAuth()
	body, _ := json.Marshal(map[string]interface{}{
		"productId": id,
		"name":      name,
		"vendor":    vendor,
	})
	if err := provisionPost(convexURL+"/devices/provision-register-product", token, body); err != nil {
		fmt.Fprintf(os.Stderr, "product: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Registered product %q (%s)\n", id, name)
}

// provisionPost is a small bearer POST that surfaces the server's error
// message on non-2xx.
func provisionPost(url, token string, body []byte) error {
	req, err := newBearerRequest(http.MethodPost, url, token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func printProvisionQR(payload string) {
	if pairQROptOut() {
		return
	}
	qrterminal.GenerateWithConfig(payload, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 2,
	})
}
