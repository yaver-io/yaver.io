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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
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
	case "flash":
		runProvisionFlash(rest)
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

  yaver provision qr <seed.json> [--png <out.png>]
                                    Re-print (or save) the scannable QR

  yaver provision flash --seed <seed.json> --boot <mountpoint>
                                    Install a seed onto a device's MOUNTED
                                    boot partition (writes yaver-provision.json)
                                    + print the label QR. (Flash the base
                                    image with your usual tool first.)

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
	var productID, model, platform, vendor, name, out, outDir, manifestPath string
	count := 1
	register := true
	forceRegisterProduct := false
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
		case "--vendor":
			i++
			if i < len(args) {
				vendor = args[i]
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
		case "--out-dir":
			i++
			if i < len(args) {
				outDir = args[i]
			}
		case "--manifest":
			i++
			if i < len(args) {
				manifestPath = args[i]
			}
		case "--count":
			i++
			if i < len(args) {
				if n, perr := strconv.Atoi(args[i]); perr == nil && n > 0 {
					count = n
				}
			}
		case "--register-product":
			forceRegisterProduct = true
		case "--no-register":
			register = false
		}
	}

	// A yaver.provision.yaml (explicit --manifest, else auto-detected in CWD)
	// supplies product/model/vendor/platform + the services summary so a
	// builder doesn't repeat flags per device. Explicit flags still win.
	manifest := loadMintManifest(manifestPath)
	if manifest != nil {
		if productID == "" {
			productID = manifest.Product
		}
		if model == "" {
			model = manifest.Model
		}
		if vendor == "" {
			vendor = manifest.Vendor
		}
		if platform == "" {
			platform = manifest.Platform
		}
	}
	if platform == "" {
		platform = "linux"
	}

	convexURL, token := "", ""
	if register {
		convexURL, token = provisionAuth()
	} else {
		if cfg, err := LoadConfig(); err == nil && cfg != nil {
			convexURL = strings.TrimRight(cfg.ConvexSiteURL, "/")
		}
		if convexURL == "" {
			convexURL = strings.TrimRight(defaultConvexSiteURL, "/")
		}
	}

	// Register the product once up front so the claim UI shows a friendly
	// model name. Triggered by a manifest, --register-product, or any time
	// we have both a slug and a display name and we're online.
	if register && productID != "" && (manifest != nil || forceRegisterProduct) && model != "" {
		var services []string
		if manifest != nil {
			services = manifest.Services
		}
		body, _ := json.Marshal(map[string]interface{}{
			"productId":       productID,
			"name":            model,
			"vendor":          vendor,
			"defaultServices": services,
		})
		if err := provisionPost(convexURL+"/devices/provision-register-product", token, body); err != nil {
			fmt.Fprintf(os.Stderr, "mint: register product failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Product %q (%s) registered\n", productID, model)
	}

	// Bulk path: write N seeds into --out-dir plus a CSV manifest a factory
	// line / label printer can consume (raw QR payload per row). We don't
	// spew N terminal QRs; single mint still prints one.
	if count > 1 {
		runBulkMint(count, productID, model, platform, name, vendor, outDir, convexURL, token, register)
		return
	}

	seed, err := GenerateProvisionSeed(productID, model, platform, convexURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint: %v\n", err)
		os.Exit(1)
	}
	if register {
		if err := registerMintedSeed(convexURL, token, seed, name, platform); err != nil {
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

// loadMintManifest resolves the manifest for a mint run: explicit path, or
// auto-detected yaver.provision.yaml in the current directory. Returns nil
// (silently) when none is present — flags-only mint stays supported.
func loadMintManifest(explicitPath string) *ProvisionManifest {
	if strings.TrimSpace(explicitPath) != "" {
		dir := explicitPath
		if !strings.HasSuffix(explicitPath, ".yaml") && !strings.HasSuffix(explicitPath, ".yml") {
			// treat as a directory
		} else {
			dir = filepath.Dir(explicitPath)
		}
		m, err := LoadProvisionManifest(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mint: %v\n", err)
			os.Exit(1)
		}
		return m
	}
	m, _ := LoadProvisionManifest(".")
	return m
}

// registerMintedSeed POSTs a single minted device identity to Convex.
func registerMintedSeed(convexURL, token string, seed *ProvisionSeed, name, platform string) error {
	pub, err := seed.PublicKeyBase64()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]interface{}{
		"deviceId":        seed.DeviceID,
		"publicKey":       pub,
		"claimSecretHash": claimSecretHashHex(seed.ClaimSecret),
		"productId":       seed.ProductID,
		"name":            name,
		"platform":        platform,
	})
	return provisionPost(convexURL+"/devices/provision-mint", token, body)
}

// runBulkMint mints `count` devices into outDir and writes a CSV the label
// printer / factory tooling consumes. One seed file + one CSV row each.
func runBulkMint(count int, productID, model, platform, name, vendor, outDir, convexURL, token string, register bool) {
	if outDir == "" {
		outDir = "./provision-batch"
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "mint: create out dir: %v\n", err)
		os.Exit(1)
	}
	csvPath := filepath.Join(outDir, "labels.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint: create csv: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Fprintln(f, "deviceId,productId,model,seedPath,qrPayload")

	var labels []labelRow
	minted := 0
	for i := 0; i < count; i++ {
		seed, gerr := GenerateProvisionSeed(productID, model, platform, convexURL)
		if gerr != nil {
			fmt.Fprintf(os.Stderr, "mint: device %d: %v\n", i+1, gerr)
			os.Exit(1)
		}
		if register {
			if rerr := registerMintedSeed(convexURL, token, seed, name, platform); rerr != nil {
				fmt.Fprintf(os.Stderr, "mint: device %d register failed: %v\n", i+1, rerr)
				os.Exit(1)
			}
		}
		seedPath := filepath.Join(outDir, fmt.Sprintf("provision-%s.json", seed.DeviceID))
		if werr := writeProvisionSeed(seedPath, seed); werr != nil {
			fmt.Fprintf(os.Stderr, "mint: device %d write seed: %v\n", i+1, werr)
			os.Exit(1)
		}
		uri, uerr := seed.ProvisionQRURI()
		if uerr != nil {
			fmt.Fprintf(os.Stderr, "mint: device %d qr: %v\n", i+1, uerr)
			os.Exit(1)
		}
		fmt.Fprintf(f, "%s,%s,%s,%s,%s\n", seed.DeviceID, productID, csvField(model), seedPath, uri)
		labels = append(labels, labelRow{DeviceID: seed.DeviceID, Model: model, URI: uri})
		minted++
	}

	// A printable HTML sheet with embedded QR images — open it and print to a
	// label roll / sticker sheet. Self-contained (data-URI PNGs, no network).
	htmlPath := filepath.Join(outDir, "labels.html")
	if werr := writeLabelSheet(htmlPath, productID, model, labels); werr != nil {
		fmt.Fprintf(os.Stderr, "mint: write label sheet: %v\n", werr)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Minted %d devices%s\n", minted, regSuffix(register))
	fmt.Printf("  Seeds:  %s/provision-<id>.json  (flash each as yaver-provision.json)\n", outDir)
	fmt.Printf("  Labels: %s  (printable QR sheet — open in a browser and print)\n", htmlPath)
	fmt.Printf("          %s  (deviceId,productId,model,seedPath,qrPayload for tooling)\n", csvPath)
	fmt.Println()
}

// labelRow is one device's printable label data.
type labelRow struct {
	DeviceID string
	Model    string
	URI      string
}

// qrPNGBytes renders a QR payload to PNG bytes via rsc.io/qr (already in the
// module graph — no extra download). Scale gives crisp, printable pixels.
func qrPNGBytes(payload string) ([]byte, error) {
	code, err := qr.Encode(payload, qr.M)
	if err != nil {
		return nil, err
	}
	code.Scale = 6
	return code.PNG(), nil
}

// qrPNGDataURI returns a data: URI for inline-HTML embedding.
func qrPNGDataURI(payload string) (string, error) {
	png, err := qrPNGBytes(payload)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// writeLabelSheet emits a self-contained printable HTML grid of QR labels.
func writeLabelSheet(path, productID, model string, rows []labelRow) error {
	var b strings.Builder
	title := model
	if title == "" {
		title = productID
	}
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString(" — Yaver provisioning labels</title><style>")
	b.WriteString("body{font-family:system-ui,sans-serif;margin:16px}")
	b.WriteString(".grid{display:flex;flex-wrap:wrap;gap:10px}")
	b.WriteString(".label{border:1px solid #ccc;border-radius:8px;padding:10px;width:200px;text-align:center;page-break-inside:avoid}")
	b.WriteString(".label img{width:170px;height:170px}")
	b.WriteString(".m{font-weight:600;font-size:13px;margin-top:6px}")
	b.WriteString(".d{font-family:ui-monospace,monospace;font-size:10px;color:#666;word-break:break-all}")
	b.WriteString("@media print{.label{width:30%}}</style></head><body>")
	b.WriteString("<h2>" + html.EscapeString(title) + " — scan to claim</h2><div class=\"grid\">")
	for _, r := range rows {
		uri, err := qrPNGDataURI(r.URI)
		if err != nil {
			return err
		}
		b.WriteString("<div class=\"label\"><img src=\"")
		b.WriteString(uri)
		b.WriteString("\" alt=\"QR\"><div class=\"m\">")
		b.WriteString(html.EscapeString(firstNonEmptyStr(r.Model, title)))
		b.WriteString("</div><div class=\"d\">")
		b.WriteString(html.EscapeString(r.DeviceID))
		b.WriteString("</div></div>")
	}
	b.WriteString("</div></body></html>")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// csvField quotes a field if it contains a comma or quote.
func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

func regSuffix(registered bool) string {
	if registered {
		return " (registered with Convex)"
	}
	return " (NOT registered — run mint online before shipping)"
}

func runProvisionQR(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver provision qr <seed.json> [--png <out.png>]")
		os.Exit(1)
	}
	seedPath := args[0]
	pngOut := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--png" {
			i++
			if i < len(args) {
				pngOut = args[i]
			}
		}
	}
	seed, err := readProvisionSeed(seedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qr: %v\n", err)
		os.Exit(1)
	}
	uri, err := seed.ProvisionQRURI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qr: %v\n", err)
		os.Exit(1)
	}
	if pngOut != "" {
		png, perr := qrPNGBytes(uri)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "qr: render png: %v\n", perr)
			os.Exit(1)
		}
		if werr := os.WriteFile(pngOut, png, 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "qr: write png: %v\n", werr)
			os.Exit(1)
		}
		fmt.Printf("✓ QR written to %s\n", pngOut)
	}
	printProvisionQR(uri)
	fmt.Printf("\n  %s\n", uri)
}

// runProvisionFlash installs a minted seed onto a device's MOUNTED boot
// partition (the FAT volume your flasher created). It deliberately does NOT
// touch raw block devices / dd an image — that's destructive and risks
// wiping the wrong disk. Flash the base image with your usual tool, mount
// the boot partition, then point this at it.
func runProvisionFlash(args []string) {
	var seedPath, bootDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--seed":
			i++
			if i < len(args) {
				seedPath = args[i]
			}
		case "--boot":
			i++
			if i < len(args) {
				bootDir = args[i]
			}
		}
	}
	if seedPath == "" || bootDir == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver provision flash --seed <seed.json> --boot <mounted-boot-partition>")
		os.Exit(1)
	}
	seed, err := readProvisionSeed(seedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flash: %v\n", err)
		os.Exit(1)
	}
	info, err := os.Stat(bootDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "flash: --boot %q is not a mounted directory\n", bootDir)
		os.Exit(1)
	}
	dest := filepath.Join(bootDir, "yaver-provision.json")
	if werr := writeProvisionSeed(dest, seed); werr != nil {
		fmt.Fprintf(os.Stderr, "flash: write seed: %v\n", werr)
		os.Exit(1)
	}
	uri, _ := seed.ProvisionQRURI()
	fmt.Printf("\n✓ Installed seed for device %s onto %s\n", seed.DeviceID, dest)
	fmt.Println("  On first boot the box reads this, attests, and self-credentials.")
	fmt.Println("  Print this QR on the device label:")
	fmt.Println()
	if uri != "" {
		printProvisionQR(uri)
	}
	fmt.Println()
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
