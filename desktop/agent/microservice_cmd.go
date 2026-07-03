package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

func runMicroservice(args []string) {
	if len(args) == 0 {
		microserviceUsage()
		return
	}
	switch args[0] {
	case "detect":
		runMicroserviceDetect(args[1:])
	case "wrap":
		runMicroserviceWrap(args[1:])
	case "status":
		runMicroserviceStatus(args[1:])
	case "down", "stop":
		runMicroserviceDown(args[1:])
	case "help", "-h", "--help":
		microserviceUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver microservice %s\n\n", args[0])
		microserviceUsage()
		os.Exit(1)
	}
}

func microserviceUsage() {
	fmt.Print(`yaver microservice — wrap a repo worker/service with Yaver.

This writes yaver.companion.yaml and can arm it as a reboot-durable service:
systemd user unit on Linux, launchd LaunchAgent on macOS. MCP exposes the same
flow as microservice_detect / microservice_wrap / microservice_status.

Usage:
  yaver microservice detect [--repo DIR] [--project NAME] [--json]
      Analyze a repo and print a proposed yaver.companion.yaml.

  yaver microservice wrap --command "npm run worker" [--repo DIR] [--name NAME]
                           [--project NAME] [--port N] [--env-file .env]
                           [--env-vault PROJECT] [--write] [--arm]
                           [--overwrite] [--no-durable] [--ai-wrap] [--json]
      Create/update yaver.companion.yaml. With --arm, install/start the
      companion using the OS supervisor when durable=true.

  yaver microservice status [--json] <project>
      Show live companion status for a wrapped microservice.

  yaver microservice down [--json] <project>
      Stop/remove the project's companion schedules and durable units.

Examples:
  yaver microservice detect --repo .
  yaver microservice wrap --command "npm run worker" --name queue --write
  yaver microservice wrap --command "node worker.js" --env-file .env --write --arm
`)
}

func runMicroserviceDetect(args []string) {
	fs := flag.NewFlagSet("microservice detect", flag.ExitOnError)
	repo := fs.String("repo", "", "repo directory (default current directory)")
	project := fs.String("project", "", "companion project slug")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)

	res, err := MicroserviceDetect(*repo, *project)
	if err != nil {
		fatalMicroservice(err)
	}
	printMicroserviceResult(res, *asJSON)
}

func runMicroserviceWrap(args []string) {
	fs := flag.NewFlagSet("microservice wrap", flag.ExitOnError)
	req := MicroserviceWrapRequest{}
	fs.StringVar(&req.Repo, "repo", "", "repo directory (default current directory)")
	fs.StringVar(&req.Project, "project", "", "companion project slug")
	fs.StringVar(&req.Name, "name", "", "service name")
	fs.StringVar(&req.Command, "command", "", "service command")
	fs.StringVar(&req.Workdir, "workdir", "", "service workdir, repo-relative or absolute")
	fs.IntVar(&req.Port, "port", 0, "service port")
	fs.StringVar(&req.EnvVault, "env-vault", "", "vault project namespace to inject on-device")
	fs.StringVar(&req.EnvFile, "env-file", "", "dotenv file to inject on-device")
	fs.BoolVar(&req.Write, "write", false, "write yaver.companion.yaml")
	fs.BoolVar(&req.Arm, "arm", false, "install/start the companion after writing")
	fs.BoolVar(&req.Overwrite, "overwrite", false, "replace an existing yaver.companion.yaml")
	fs.BoolVar(&req.UseShell, "shell", false, "force sh -lc/cmd /C")
	fs.BoolVar(&req.AIWrap, "ai-wrap", false, "mark service as AI-wrapped")
	fs.StringVar(&req.AIWorkKind, "ai-work-kind", "", "AI wrapper work kind")
	fs.StringVar(&req.BaseURLFrom, "base-url-from", "", "runtime base URL source, e.g. env:SUPABASE_FUNCTIONS_URL")
	fs.StringVar(&req.HealthURL, "health-url", "", "optional health-check URL")
	fs.StringVar(&req.ScheduleCron, "schedule-cron", "", "cron for health-url checks")
	noDurable := fs.Bool("no-durable", false, "run as agent child instead of OS unit")
	asJSON := fs.Bool("json", false, "print JSON")
	argsCSV := fs.String("args", "", "comma-separated argv to pass after --command binary")
	_ = fs.Parse(args)

	if *noDurable {
		v := false
		req.Durable = &v
	}
	if strings.TrimSpace(*argsCSV) != "" {
		req.Args = splitMicroserviceCSV(*argsCSV)
	}
	if req.Arm {
		req.Write = true
	}

	srv := (*HTTPServer)(nil)
	if req.Arm {
		repo, err := normalizeRepoDir(req.Repo)
		if err != nil {
			fatalMicroservice(err)
		}
		req.Repo = repo
		srv = localMicroserviceServer(repo)
	}

	res, err := MicroserviceWrap(srv, req)
	if err != nil {
		fatalMicroservice(err)
	}
	printMicroserviceResult(res, *asJSON)
}

func runMicroserviceStatus(args []string) {
	project, asJSON := parseMicroserviceProjectAndJSON(args)
	if project == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver microservice status [--json] <project>")
		os.Exit(2)
	}
	status, err := localMicroserviceServer("").companionEngine().Status(project)
	if err != nil {
		fatalMicroservice(err)
	}
	if asJSON {
		printJSON(status)
		return
	}
	printCompanionStatus(status)
}

func runMicroserviceDown(args []string) {
	project, asJSON := parseMicroserviceProjectAndJSON(args)
	if project == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver microservice down [--json] <project>")
		os.Exit(2)
	}
	if err := localMicroserviceServer("").companionEngine().Down(project); err != nil {
		fatalMicroservice(err)
	}
	if asJSON {
		printJSON(map[string]interface{}{"ok": true, "project": project})
		return
	}
	fmt.Printf("Microservice %q stopped and disarmed.\n", project)
}

func localMicroserviceServer(repo string) *HTTPServer {
	if repo == "" {
		repo, _ = os.Getwd()
	}
	cfg, _ := LoadConfig()
	deviceID := ""
	if cfg != nil {
		deviceID = cfg.DeviceID
	}
	vs := currentRuntimeVaultStore()
	if vs == nil {
		if opened, err := openVaultE(); err == nil {
			vs = opened
		}
	}
	sched := NewScheduler(nil)
	srv := &HTTPServer{
		scheduler:   sched,
		servicesMgr: NewServicesManager(repo),
		vaultStore:  vs,
	}
	srv.companion = &CompanionEngine{
		sched:    sched,
		svcs:     srv.servicesMgr,
		vault:    vs,
		deviceID: deviceID,
	}
	return srv
}

func printMicroserviceResult(res *MicroserviceWrapResult, asJSON bool) {
	if asJSON {
		printJSON(res)
		return
	}
	fmt.Printf("Project:  %s\n", res.Project)
	fmt.Printf("Manifest: %s\n", res.ManifestPath)
	if res.Written {
		fmt.Println("Written:  yes")
	} else if res.Existing {
		fmt.Println("Written:  existing manifest left unchanged")
	}
	if res.Armed {
		fmt.Println("Armed:    yes")
	}
	if len(res.Items) > 0 {
		fmt.Println()
		fmt.Println("Detected/configured:")
		for _, it := range res.Items {
			line := fmt.Sprintf("  - [%s] %s: %s", it.Kind, it.Name, it.Status)
			if it.Schedule != "" {
				line += " (" + it.Schedule + ")"
			}
			fmt.Println(line)
			if it.Reason != "" {
				fmt.Println("    " + it.Reason)
			}
		}
	}
	if len(res.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, w := range res.Warnings {
			fmt.Println("  - " + w)
		}
	}
	if !res.Written {
		fmt.Println()
		fmt.Println(res.ManifestYAML)
	}
	if len(res.Next) > 0 {
		fmt.Println()
		fmt.Println("Next:")
		for _, n := range res.Next {
			fmt.Println("  - " + n)
		}
	}
}

func printCompanionStatus(status CompanionStatus) {
	fmt.Printf("Project: %s\n", status.Project)
	fmt.Printf("Enabled: %v\n", status.Enabled)
	if len(status.Services) > 0 {
		fmt.Println("Services:")
		for _, svc := range status.Services {
			running := "stopped"
			if svc.Running {
				running = "running"
			}
			unit := svc.Unit
			if unit == "" {
				unit = "agent-child"
			}
			fmt.Printf("  - %s: %s (%s)\n", svc.Name, running, unit)
		}
	}
	if len(status.Crons) > 0 {
		fmt.Println("Crons:")
		for _, cron := range status.Crons {
			fmt.Printf("  - %s: %s next=%s last=%s\n", cron.Name, cron.Status, cron.NextRunAt, cron.LastOutcome)
		}
	}
	if len(status.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, w := range status.Warnings {
			fmt.Println("  - " + w)
		}
	}
}

func splitMicroserviceCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseMicroserviceProjectAndJSON(args []string) (string, bool) {
	var project string
	var asJSON bool
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			asJSON = true
		default:
			if strings.TrimSpace(arg) != "" && project == "" {
				project = arg
			}
		}
	}
	return project, asJSON
}

func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println("{}")
		return
	}
	fmt.Println(string(data))
}

func fatalMicroservice(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
