package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

func runFeedback(args []string) {
	if len(args) == 0 {
		printFeedbackUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "init", "setup":
		runFeedbackSetup(args[1:])
	case "list", "ls":
		runFeedbackList()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver feedback show <id>")
			os.Exit(1)
		}
		runFeedbackShow(args[1])
	case "fix":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver feedback fix <id>")
			os.Exit(1)
		}
		runFeedbackFix(args[1])
	case "delete", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver feedback delete <id>")
			os.Exit(1)
		}
		runFeedbackDelete(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown feedback subcommand: %s\n\n", args[0])
		printFeedbackUsage()
		os.Exit(1)
	}
}

func printFeedbackUsage() {
	fmt.Print(`Usage:
  yaver feedback init [--dir <path>] [--platform <name>]   Install the Feedback SDK into this project
  yaver feedback setup                                     Alias of init (kept for back-compat)
  yaver feedback list                                      List feedback reports from device testing
  yaver feedback show <id>                                 Show feedback details + transcript
  yaver feedback fix <id>                                  Create AI task from feedback (auto-generates prompt)
  yaver feedback delete <id>                               Delete a feedback report

Setup flow:
  Install Yaver once with either install point:
    npm install -g yaver-cli            (Node-first; works without the Go binary)
    brew install yaver                  (or any other platform install)
  Then, inside a project:
    yaver feedback init                 Autodetect project, install the right SDK
    yaver feedback init --platform web  Force a specific platform

Platform → package (web and mobile are deliberately separate — each is
optimized for its runtime; do not cross-install):
  web          yaver-feedback-web              (browsers)
  expo / rn    yaver-feedback-react-native     (React Native, Expo)
  flutter      yaver_feedback                  (pub.dev)
  unity        io.yaver.feedback.unity         (UPM)

Feedback is sent from:
  A) Yaver mobile app — record screen + voice while testing your build
  B) In-app SDK — trigger embedded in your app (dev mode only)

The AI agent receives screen recordings, voice transcripts, screenshots,
and a timeline — then fixes the bugs and rebuilds.
`)
}

func runFeedbackSetup(args []string) {
	fs := flag.NewFlagSet("feedback setup", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	platform := fs.String("platform", "", "Platform/framework override")
	fs.Parse(args)

	if err := performSDKAdd("feedback", *dir, *platform); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runFeedbackList() {
	resp, err := localAgentRequest("GET", "/feedback", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var reports []FeedbackSummary
	remarshal(resp, &reports)

	if len(reports) == 0 {
		fmt.Println("No feedback reports. Test a build and send feedback from your phone.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSOURCE\tPLATFORM\tVIDEO\tSCREENS\tCREATED")
	for _, r := range reports {
		video := "-"
		if r.HasVideo {
			video = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", r.ID, r.Source, r.Platform, video, r.NumScreens, r.CreatedAt[:10])
	}
	w.Flush()
}

func runFeedbackShow(id string) {
	resp, err := localAgentRequest("GET", "/feedback/"+id, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var report FeedbackReport
	remarshal(resp, &report)

	fmt.Printf("Feedback %s\n", report.ID)
	fmt.Printf("  Source:    %s\n", report.Source)
	fmt.Printf("  Device:    %s %s (%s %s)\n", report.DeviceInfo.Model, report.DeviceInfo.Platform, report.DeviceInfo.Platform, report.DeviceInfo.OSVersion)
	if report.AppVersion != "" {
		fmt.Printf("  App:       %s\n", report.AppVersion)
	}
	fmt.Printf("  Created:   %s\n", report.CreatedAt)

	if report.VideoPath != "" {
		fmt.Printf("  Video:     %s\n", report.VideoPath)
	}
	if len(report.Screenshots) > 0 {
		fmt.Printf("  Screenshots: %d\n", len(report.Screenshots))
	}

	if len(report.Timeline) > 0 {
		fmt.Println("\n  Timeline:")
		for _, e := range report.Timeline {
			min := int(e.Time) / 60
			sec := int(e.Time) % 60
			fmt.Printf("    %d:%02d [%s] %s\n", min, sec, e.Type, e.Text)
		}
	}

	if report.Transcript != "" {
		fmt.Printf("\n  Transcript:\n    %s\n", report.Transcript)
	}

	fmt.Printf("\n  Run 'yaver feedback fix %s' to create a task from this report.\n", id)
}

func runFeedbackFix(id string) {
	resp, err := localAgentRequest("POST", "/feedback/"+id+"/fix", map[string]interface{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if taskID, ok := resp["taskId"].(string); ok {
		fmt.Printf("Task created: %s\n", taskID)
		fmt.Println("The AI agent will fix the issues from the feedback report.")
	}
	if prompt, ok := resp["prompt"].(string); ok {
		fmt.Println("\nGenerated prompt:")
		fmt.Println(prompt)
	}
}

func runFeedbackDelete(id string) {
	_, err := localAgentRequest("DELETE", "/feedback/"+id, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted feedback %s\n", id)
}
