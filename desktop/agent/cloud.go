package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultCloudURL = "https://cloud.yaver.io"
)

type cloudMachineRecord struct {
	ID          string `json:"_id"`
	Status      string `json:"status"`
	MachineType string `json:"machineType"`
	ServerIP    string `json:"serverIp"`
	Hostname    string `json:"hostname"`
	Region      string `json:"region"`
	Error       string `json:"errorMessage"`
}

func runCloud(args []string) {
	if len(args) == 0 {
		printCloudUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "buy":
		runCloudBuy(args[1:])
	case "create":
		runCloudCreate(args[1:])
	case "smoke":
		runCloudSmoke(args[1:])
	case "status":
		runCloudStatus()
	case "ssh":
		runCloudSSH()
	case "destroy":
		runCloudDestroy()
	default:
		fmt.Fprintf(os.Stderr, "Unknown cloud subcommand: %s\n\n", args[0])
		printCloudUsage()
		os.Exit(1)
	}
}

func runCloudBuy(args []string) {
	fs := flag.NewFlagSet("cloud buy", flag.ExitOnError)
	wait := fs.Bool("wait", true, "Poll for the machine after checkout")
	fs.Parse(args)
	openCloudCheckout(*wait)
}

func runCloudCreate(args []string) {
	fs := flag.NewFlagSet("cloud create", flag.ExitOnError)
	wait := fs.Bool("wait", true, "Poll for the machine after checkout or create")
	skipPayment := fs.Bool("skip-payment", false, "Bypass checkout and activate the private-preview machine directly")
	fs.Parse(args)

	machine, err := activeCloudMachine()
	if err == nil && machine != nil {
		printCloudMachine(machine)
		return
	}

	if *skipPayment || strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_CLOUD_SKIP_PAYMENT")), "true") {
		activateCloudPreview(*wait)
		return
	}

	openCloudCheckout(*wait)
}

func runCloudStatus() {
	machine, err := activeCloudMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cloud status: %v\n", err)
		os.Exit(1)
	}
	if machine == nil {
		fmt.Println("No active Yaver Cloud machine found for this account.")
		fmt.Println("Run `yaver cloud create` to start the private-preview checkout flow.")
		return
	}
	printCloudMachine(machine)
}

func runCloudSmoke(args []string) {
	fs := flag.NewFlagSet("cloud smoke", flag.ExitOnError)
	skipPayment := fs.Bool("skip-payment", true, "Bypass checkout and activate the private-preview machine directly")
	fs.Parse(args)

	cfg, user, err := requireCloudPreviewUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *skipPayment {
		mode, err := activateCloudMachine(cfg.AuthToken, cfg.ConvexSiteURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "activate cloud preview: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Preview activation: %s\n", mode)
	}

	waitForCloudMachine()
	machine, err := activeCloudMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cloud smoke: %v\n", err)
		os.Exit(1)
	}
	if machine == nil {
		fmt.Fprintln(os.Stderr, "cloud smoke: no active cloud machine available")
		os.Exit(1)
	}

	slugSuffix := strings.ToLower(time.Now().UTC().Format("20060102-150405"))
	project, err := CreatePhoneProject(PhoneCreateSpec{
		Name:     "Cloud Smoke Todos " + slugSuffix,
		Template: "todos",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create smoke project: %v\n", err)
		os.Exit(1)
	}

	adapter, err := PhoneAdapter(project.Slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open smoke project: %v\n", err)
		os.Exit(1)
	}
	if _, err := adapter.Insert("todos", map[string]interface{}{
		"id":       "smoke-" + slugSuffix,
		"title":    "Yaver Cloud smoke test",
		"done":     false,
		"owner_id": "alice",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "seed smoke todo: %v\n", err)
		os.Exit(1)
	}

	bundle, err := ExportPhoneProjectWithOptions(project.Slug, PhoneExportOptions{
		IncludeData:  true,
		Containerize: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "export smoke bundle: %v\n", err)
		os.Exit(1)
	}

	targetBase := cloudPreviewBaseURL()
	fmt.Printf("Pushing %s to %s\n", project.Slug, targetBase)
	result, err := pushPhoneBundle(targetBase, cfg.AuthToken, bundle, project.Slug, "overwrite", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "push smoke bundle: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Cloud smoke succeeded.")
	fmt.Printf("Account: %s\n", user.Email)
	fmt.Printf("Machine: %s\n", machine.ID)
	fmt.Printf("Project: %s\n", result.Slug)
	fmt.Printf("Browse: %s%s\n", targetBase, result.BrowseUrl)
}

func runCloudSSH() {
	machine, err := activeCloudMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cloud ssh: %v\n", err)
		os.Exit(1)
	}
	if machine == nil || strings.TrimSpace(machine.ServerIP) == "" {
		fmt.Println("No ready Yaver Cloud machine found yet.")
		fmt.Println("Run `yaver cloud status` and wait for the machine to become active.")
		return
	}
	fmt.Printf("SSH target: root@%s\n", machine.ServerIP)
	fmt.Printf("Host: %s\n", machine.Hostname)
	fmt.Println("Open an SSH session with:")
	fmt.Printf("  ssh root@%s\n", machine.ServerIP)
}

func runCloudDestroy() {
	fmt.Println("Cloud destroy is not automated yet.")
	fmt.Println("For now, cancel from the hosted checkout/provider side or remove the Hetzner machine manually.")
}

func openCloudCheckout(wait bool) {
	cfg, user, err := requireCloudPreviewUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	checkoutURL, mode, err := createCloudCheckout(cfg.AuthToken, cfg.ConvexSiteURL)
	if err != nil {
		checkoutURL = strings.TrimSpace(os.Getenv("YAVER_CLOUD_CHECKOUT_URL"))
		if checkoutURL == "" {
			checkoutURL = strings.TrimSpace(os.Getenv("NEXT_PUBLIC_LEMONSQUEEZY_YAVER_CLOUD_URL"))
		}
		if checkoutURL == "" {
			base := strings.TrimRight(cfg.WebBaseURL, "/")
			if base == "" {
				base = "https://yaver.io"
			}
			checkoutURL = base + "/pricing"
		}
		mode = "fallback"
	}

	fmt.Println("Yaver Cloud — private preview")
	fmt.Println("-----------------------------")
	fmt.Printf("Signed in as: %s\n", user.Email)
	fmt.Printf("Billing mode: %s\n", mode)
	fmt.Printf("Checkout URL: %s\n", checkoutURL)
	fmt.Println()

	r := bufio.NewReader(os.Stdin)
	if promptYes(r, "Open checkout in your browser now?", true) {
		if err := accountOpenBrowser(checkoutURL); err != nil {
			fmt.Printf("Could not open the browser automatically: %v\n", err)
		}
	} else {
		fmt.Println("Open the URL above when ready.")
	}

	if !wait {
		return
	}

	fmt.Println()
	fmt.Println("Waiting for the checkout webhook to provision your machine...")
	waitForCloudMachine()
}

func cloudPreviewBaseURL() string {
	if base := strings.TrimSpace(os.Getenv("YAVER_CLOUD_PREVIEW_BASE_URL")); base != "" {
		return strings.TrimRight(base, "/")
	}
	if base := strings.TrimSpace(os.Getenv("YAVER_CLOUD_BASE_URL")); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultCloudURL
}

func activateCloudPreview(wait bool) {
	cfg, user, err := requireCloudPreviewUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mode, err := activateCloudMachine(cfg.AuthToken, cfg.ConvexSiteURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "activate cloud preview: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Yaver Cloud — private preview")
	fmt.Println("-----------------------------")
	fmt.Printf("Signed in as: %s\n", user.Email)
	fmt.Printf("Activation mode: %s\n", mode)

	if !wait {
		return
	}

	fmt.Println()
	fmt.Println("Waiting for the preview machine to become active...")
	waitForCloudMachine()
}

func requireCloudPreviewUser() (*Config, *UserInfo, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil, nil, fmt.Errorf("load config: sign in first with `yaver auth`")
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, nil, fmt.Errorf("missing auth token: sign in first with `yaver auth`")
	}
	baseURL := strings.TrimSpace(cfg.ConvexSiteURL)
	if baseURL == "" {
		baseURL = defaultConvexSiteURL
	}
	user, err := ValidateTokenInfo(baseURL, cfg.AuthToken)
	if err != nil {
		return nil, nil, fmt.Errorf("validate token: %w", err)
	}
	if strings.TrimSpace(user.Email) == "" {
		return nil, nil, fmt.Errorf("authenticated account is missing an email address")
	}
	cfg.ConvexSiteURL = baseURL
	return cfg, user, nil
}

func waitForCloudMachine() {
	deadline := time.Now().Add(12 * time.Minute)
	lastStatus := ""
	for time.Now().Before(deadline) {
		machine, err := activeCloudMachine()
		if err == nil && machine != nil {
			if machine.Status != lastStatus {
				printCloudMachine(machine)
				lastStatus = machine.Status
			}
			if machine.Status == "active" {
				fmt.Println()
				fmt.Println("Machine is ready.")
				return
			}
			if machine.Status == "error" {
				fmt.Println()
				fmt.Println("Provisioning ended in error.")
				return
			}
		}
		time.Sleep(5 * time.Second)
	}
	fmt.Println("Timed out waiting for the cloud machine. Run `yaver cloud status` later.")
}

func createCloudCheckout(authToken, convexBase string) (string, string, error) {
	base := strings.TrimRight(strings.TrimSpace(convexBase), "/")
	if base == "" {
		base = defaultConvexSiteURL
	}
	reqBody, _ := json.Marshal(map[string]string{"region": "eu"})
	req, err := newBearerRequest(http.MethodPost, base+"/billing/yaver-cloud/checkout", authToken, bytes.NewReader(reqBody))
	if err != nil {
		return "", "", fmt.Errorf("create checkout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("checkout request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("checkout request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("decode checkout response: %w", err)
	}
	if strings.TrimSpace(result.URL) == "" {
		return "", "", fmt.Errorf("checkout URL missing in response")
	}
	if strings.TrimSpace(result.Mode) == "" {
		result.Mode = "sandbox"
	}
	return result.URL, result.Mode, nil
}

func activateCloudMachine(authToken, convexBase string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(convexBase), "/")
	if base == "" {
		base = defaultConvexSiteURL
	}
	reqBody, _ := json.Marshal(map[string]string{"region": "eu"})
	req, err := newBearerRequest(http.MethodPost, base+"/billing/yaver-cloud/dev-activate", authToken, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create activation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("activation request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("activation request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode activation response: %w", err)
	}
	if strings.TrimSpace(result.Mode) == "" {
		result.Mode = "dev-bypass"
	}
	return result.Mode, nil
}

func activeCloudMachine() (*cloudMachineRecord, error) {
	cfg, _, err := requireCloudPreviewUser()
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.ConvexSiteURL, "/")
	req, err := newBearerRequest(http.MethodGet, base+"/machines", cfg.AuthToken, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch machines: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch machines failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Machines []cloudMachineRecord `json:"machines"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode machines: %w", err)
	}
	if len(result.Machines) == 0 {
		return nil, nil
	}
	return &result.Machines[0], nil
}

func printCloudMachine(machine *cloudMachineRecord) {
	if machine == nil {
		return
	}
	fmt.Println()
	fmt.Println("Yaver Cloud machine")
	fmt.Println("-------------------")
	fmt.Printf("ID: %s\n", machine.ID)
	fmt.Printf("Type: %s\n", machine.MachineType)
	fmt.Printf("Status: %s\n", machine.Status)
	if machine.Region != "" {
		fmt.Printf("Region: %s\n", machine.Region)
	}
	if machine.Hostname != "" {
		fmt.Printf("Hostname: %s\n", machine.Hostname)
	}
	if machine.ServerIP != "" {
		fmt.Printf("IP: %s\n", machine.ServerIP)
		fmt.Printf("Base URL: %s\n", cloudPreviewBaseURL())
	}
	if machine.Error != "" {
		fmt.Printf("Error: %s\n", machine.Error)
	}
}

func printCloudUsage() {
	fmt.Print(`Usage:
  yaver cloud buy      Open the web checkout for the private-preview cloud flow
  yaver cloud create   Open checkout if needed, then wait for your cloud machine
  yaver cloud smoke    Activate preview if needed, push a dummy todos backend, print the browse URL
  yaver cloud status   Show the current cloud machine state
  yaver cloud ssh      Print the SSH target for the current cloud machine
  yaver cloud destroy  Show the current manual teardown note

Notes:
  - Yaver Cloud purchase is web-only for now.
  - CLI opens the hosted checkout and then polls the backend so the flow does not stall.
  - Use 'yaver cloud create --skip-payment' to test the shared preview machine without Lemon Squeezy.
`)
}
