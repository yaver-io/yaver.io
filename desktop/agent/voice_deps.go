package main

// voice_deps.go — `yaver voice deps`: make the free/offline voice path
// install itself, so users never touch ffmpeg/whisper.cpp by hand.
//
// Three host dependencies power the FREE path (cloud Deepgram/OpenAI need
// none of them — just an API key):
//   1. ffmpeg          — mic capture for `yaver voice listen` / test mode
//   2. whisper.cpp CLI — free/offline STT (provider "local")
//   3. a ggml model    — whisper weights under ~/.yaver/models/
//
// Invoked three ways, all sharing ensureVoiceDeps:
//   - npm postinstall  → `yaver voice deps --install --quiet` (best-effort)
//   - `yaver voice deps [--install]`  (manual status / install)
//   - `yaver voice listen|test` first run, when local is picked but unready
//
// Best-effort + idempotent: a missing package manager or failed download
// downgrades to a printed hint, never an error/panic (safe in postinstall).

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// defaultWhisperModelURL is the ~78MB English base model — a good default
// for dictation. The repo-bundled tiny model is the offline fallback.
const defaultWhisperModelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
const defaultWhisperModelName = "ggml-base.en.bin"

// voiceModelDir is ~/.yaver/models (created on demand).
func voiceModelDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yaver", "models")
}

type voiceDepState struct {
	name   string
	ok     bool
	detail string
}

// voiceDepStates probes all three deps without changing anything.
func voiceDepStates() []voiceDepState {
	out := make([]voiceDepState, 0, 3)
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		out = append(out, voiceDepState{"ffmpeg", true, p})
	} else {
		out = append(out, voiceDepState{"ffmpeg", false, "not found (mic capture)"})
	}
	if bin := localWhisperBin(); bin != "" {
		out = append(out, voiceDepState{"whisper.cpp", true, bin})
	} else {
		out = append(out, voiceDepState{"whisper.cpp", false, "not found (free/offline STT)"})
	}
	if m := localWhisperModel(); m != "" {
		out = append(out, voiceDepState{"whisper model", true, m})
	} else {
		out = append(out, voiceDepState{"whisper model", false, "not found (STT weights)"})
	}
	return out
}

// runVoiceDeps handles `yaver voice deps [--install] [--model <url>] [--quiet]`.
func runVoiceDeps(args []string) {
	install, quiet := false, false
	modelURL := defaultWhisperModelURL
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--install", "-i":
			install = true
		case "--quiet", "-q":
			quiet = true
		case "--model":
			if i+1 < len(args) {
				modelURL = args[i+1]
				i++
			}
		case "-h", "--help", "help":
			fmt.Println("yaver voice deps — check/install local voice dependencies")
			fmt.Println()
			fmt.Println("  yaver voice deps             show ffmpeg / whisper.cpp / model status")
			fmt.Println("  yaver voice deps --install   install whatever is missing (best-effort)")
			fmt.Println("  yaver voice deps --install --model <url>   use a specific ggml model")
			fmt.Println()
			fmt.Println("Only the FREE/offline path needs these. Deepgram/OpenAI need just a key.")
			return
		}
	}

	if !install {
		fmt.Println("voice dependencies (free/offline path):")
		allOK := true
		for _, s := range voiceDepStates() {
			mark := "✓"
			if !s.ok {
				mark, allOK = "✗", false
			}
			fmt.Printf("  %s %-14s %s\n", mark, s.name, s.detail)
		}
		if allOK {
			fmt.Println("\nAll set — `yaver voice listen` / `yaver voice test` work offline, $0.")
		} else {
			fmt.Println("\nRun `yaver voice deps --install` to provision the missing pieces.")
			fmt.Println("(Cloud STT via Deepgram/OpenAI needs none of these — just a key.)")
		}
		return
	}

	changed := ensureVoiceDeps(modelURL, quiet)
	if !quiet {
		if changed {
			fmt.Println("\nDone. Free/offline voice (provider=local) is ready.")
		} else {
			fmt.Println("\nNothing to do — all voice dependencies already present.")
		}
	}
}

// ensureVoiceDeps installs any missing dependency. Best-effort: one
// failure prints a hint and moves on, never aborting. Returns true if it
// changed anything. Safe from npm postinstall (no panic / no os.Exit).
func ensureVoiceDeps(modelURL string, quiet bool) bool {
	logf := func(format string, a ...interface{}) {
		if !quiet {
			fmt.Printf(format+"\n", a...)
		}
	}
	changed := false

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		logf("[voice] installing ffmpeg (mic capture)…")
		if installSystemPackage("ffmpeg") {
			changed = true
			logf("[voice] ffmpeg installed.")
		} else {
			logf("[voice] auto-install of ffmpeg failed — install manually: %s", pkgHint("ffmpeg"))
		}
	}

	if localWhisperBin() == "" {
		logf("[voice] installing whisper.cpp (free/offline STT)…")
		if installSystemPackage("whisper-cpp") {
			changed = true
			logf("[voice] whisper.cpp installed.")
		} else {
			logf("[voice] auto-install of whisper.cpp failed — %s", pkgHint("whisper-cpp"))
		}
	}

	if localWhisperModel() == "" {
		dir := voiceModelDir()
		if dir != "" {
			dest := filepath.Join(dir, defaultWhisperModelName)
			logf("[voice] downloading whisper model → %s …", dest)
			if err := downloadFileTo(modelURL, dest); err != nil {
				logf("[voice] model download failed: %v", err)
				if copyBundledTinyModel(dir) {
					changed = true
					logf("[voice] using bundled tiny model instead.")
				}
			} else {
				changed = true
				logf("[voice] model ready.")
			}
		}
	}

	return changed
}

// installSystemPackage installs one package via the platform's manager.
// Returns true on success; false → caller tells the user to do it manually.
func installSystemPackage(pkg string) bool {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err != nil {
			return false
		}
		return runSilent("brew", "install", pkg)
	case "linux":
		if pkg == "whisper-cpp" {
			return false // not in apt/dnf; user must build it
		}
		if _, err := exec.LookPath("apt-get"); err == nil {
			return runSilent("sudo", "apt-get", "install", "-y", pkg)
		}
		if _, err := exec.LookPath("dnf"); err == nil {
			return runSilent("sudo", "dnf", "install", "-y", pkg)
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return runSilent("sudo", "pacman", "-S", "--noconfirm", pkg)
		}
		return false
	default:
		return false
	}
}

func pkgHint(pkg string) string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install " + pkg
	case "linux":
		if pkg == "whisper-cpp" {
			return "build whisper.cpp from github.com/ggerganov/whisper.cpp (no apt package)"
		}
		return "apt-get install " + pkg + " (or your distro's equivalent)"
	default:
		return "install " + pkg + " for your platform"
	}
}

func runSilent(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// downloadFileTo fetches url → dest atomically (.part rename), 10-min
// timeout for the ~78MB model.
func downloadFileTo(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// copyBundledTinyModel copies the repo's mobile ggml-whisper-tiny.bin into
// the agent model dir as an offline fallback when the download fails.
func copyBundledTinyModel(destDir string) bool {
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	src := filepath.Join(home, "Workspace", "yaver.io", "mobile", "assets", "models", "ggml-whisper-tiny.bin")
	if !fileExists(src) {
		return false
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return false
	}
	in, err := os.Open(src)
	if err != nil {
		return false
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(destDir, "ggml-whisper-tiny.bin"))
	if err != nil {
		return false
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err == nil
}

// voiceDepsReady reports whether the free path is fully provisioned.
func voiceDepsReady() bool {
	for _, s := range voiceDepStates() {
		if !s.ok {
			return false
		}
	}
	return true
}
