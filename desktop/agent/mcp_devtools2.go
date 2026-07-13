package main

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Database tools
// ---------------------------------------------------------------------------

func mcpDBQuery(driver, dsn, query string) interface{} {
	switch driver {
	case "sqlite", "sqlite3":
		if dsn == "" {
			return map[string]interface{}{"error": "dsn required (path to .db file)"}
		}
		out, err := runCmd("sqlite3", "-header", "-column", dsn, query)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("sqlite3: %s — %s", err, out)}
		}
		return map[string]interface{}{"result": out, "driver": "sqlite3"}

	case "postgres", "postgresql", "psql":
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			return map[string]interface{}{"error": "dsn required (e.g. postgres://user:pass@host/db) or set DATABASE_URL"}
		}
		out, err := runCmd("psql", dsn, "-c", query)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("psql: %s — %s", err, out)}
		}
		return map[string]interface{}{"result": out, "driver": "postgres"}

	case "mysql":
		if dsn == "" {
			return map[string]interface{}{"error": "dsn required (e.g. -u user -p pass -h host dbname)"}
		}
		out, err := runCmd("sh", "-c", fmt.Sprintf("mysql %s -e %q", dsn, query))
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("mysql: %s — %s", err, out)}
		}
		return map[string]interface{}{"result": out, "driver": "mysql"}

	case "redis":
		if dsn == "" {
			dsn = "localhost:6379"
		}
		parts := strings.Fields(query)
		args := []string{"-h", dsn}
		if strings.Contains(dsn, ":") {
			hp := strings.SplitN(dsn, ":", 2)
			args = []string{"-h", hp[0], "-p", hp[1]}
		}
		args = append(args, parts...)
		out, err := runCmd("redis-cli", args...)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("redis-cli: %s — %s", err, out)}
		}
		return map[string]interface{}{"result": out, "driver": "redis"}

	default:
		return map[string]interface{}{"error": "unsupported driver: " + driver + ". Use: sqlite, postgres, mysql, redis"}
	}
}

func mcpDBSchemas(driver, dsn string) interface{} {
	switch driver {
	case "sqlite", "sqlite3":
		out, err := runCmd("sqlite3", dsn, ".schema")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"schema": out}
	case "postgres", "postgresql":
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		out, err := runCmd("psql", dsn, "-c", "\\dt+")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"schema": out}
	case "mysql":
		out, err := runCmd("sh", "-c", fmt.Sprintf("mysql %s -e 'SHOW TABLES; SHOW CREATE TABLE'", dsn))
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"schema": out}
	default:
		return map[string]interface{}{"error": "unsupported driver: " + driver}
	}
}

// ---------------------------------------------------------------------------
// Network diagnostics
// ---------------------------------------------------------------------------

func mcpDNSLookup(host, recordType string) interface{} {
	if recordType == "" {
		recordType = "A"
	}
	out, err := runCmd("dig", "+short", host, recordType)
	if err != nil {
		// Fallback to nslookup
		out, err = runCmd("nslookup", host)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	return map[string]interface{}{"host": host, "type": recordType, "records": out}
}

func mcpPing(host string, count int) interface{} {
	if count <= 0 {
		count = 4
	}
	out, err := runCmd("ping", "-c", strconv.Itoa(count), host)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"output": out}
}

func mcpSSLCheck(host string) interface{} {
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", host, &tls.Config{})
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "valid": false}
	}
	defer conn.Close()

	cert := conn.ConnectionState().PeerCertificates[0]
	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	return map[string]interface{}{
		"valid":      true,
		"subject":    cert.Subject.CommonName,
		"issuer":     cert.Issuer.CommonName,
		"not_before": cert.NotBefore.Format("2006-01-02"),
		"not_after":  cert.NotAfter.Format("2006-01-02"),
		"days_left":  daysLeft,
		"san":        cert.DNSNames,
	}
}

func mcpHTTPTiming(url string) interface{} {
	if err := guardOutboundHTTPURL(url); err != nil { // A3: no metadata/link-local SSRF
		return map[string]interface{}{"error": err.Error()}
	}
	start := time.Now()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	duration := time.Since(start)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	return map[string]interface{}{
		"url":         url,
		"status":      resp.StatusCode,
		"latency_ms":  duration.Milliseconds(),
		"server":      resp.Header.Get("Server"),
		"content_type": resp.Header.Get("Content-Type"),
	}
}

// ---------------------------------------------------------------------------
// Data tools
// ---------------------------------------------------------------------------

func mcpBase64(action, input string) interface{} {
	switch action {
	case "encode":
		return map[string]interface{}{"result": base64.StdEncoding.EncodeToString([]byte(input))}
	case "decode":
		decoded, err := base64.StdEncoding.DecodeString(input)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"result": string(decoded)}
	default:
		return map[string]interface{}{"error": "action must be 'encode' or 'decode'"}
	}
}

func mcpHash(algorithm, input string) interface{} {
	switch algorithm {
	case "md5":
		h := md5.Sum([]byte(input))
		return map[string]interface{}{"hash": hex.EncodeToString(h[:]), "algorithm": "md5"}
	case "sha256", "":
		h := sha256.Sum256([]byte(input))
		return map[string]interface{}{"hash": hex.EncodeToString(h[:]), "algorithm": "sha256"}
	default:
		return map[string]interface{}{"error": "unsupported algorithm: " + algorithm + ". Use: md5, sha256"}
	}
}

func mcpUUID() interface{} {
	return map[string]interface{}{"uuid": uuid.New().String()}
}

func mcpJQ(expression, input string) interface{} {
	cmd := osexec.Command("jq", expression)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("jq: %s — %s", err, string(out))}
	}
	return map[string]interface{}{"result": strings.TrimSpace(string(out))}
}

func mcpRegexTest(pattern, input string) interface{} {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return map[string]interface{}{"error": "invalid regex: " + err.Error()}
	}
	matches := re.FindAllString(input, -1)
	groups := re.FindAllStringSubmatch(input, -1)
	return map[string]interface{}{
		"matches": matches,
		"groups":  groups,
		"count":   len(matches),
	}
}

// ---------------------------------------------------------------------------
// Archive tools
// ---------------------------------------------------------------------------

func mcpArchiveCreate(format, source, output string) interface{} {
	if source == "" {
		return map[string]interface{}{"error": "source path required"}
	}
	switch format {
	case "zip":
		if output == "" {
			output = filepath.Base(source) + ".zip"
		}
		out, err := runCmd("zip", "-r", output, source)
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": out}
		}
		return map[string]interface{}{"created": output}
	case "tar.gz", "tgz", "":
		if output == "" {
			output = filepath.Base(source) + ".tar.gz"
		}
		out, err := runCmd("tar", "czf", output, source)
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": out}
		}
		return map[string]interface{}{"created": output}
	default:
		return map[string]interface{}{"error": "unsupported format: " + format + ". Use: zip, tar.gz"}
	}
}

func mcpArchiveExtract(path, destination string) interface{} {
	if destination == "" {
		destination = "."
	}
	var out string
	var err error
	if strings.HasSuffix(path, ".zip") {
		out, err = runCmd("unzip", "-o", path, "-d", destination)
	} else {
		out, err = runCmd("tar", "xzf", path, "-C", destination)
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"extracted_to": destination}
}

// ---------------------------------------------------------------------------
// System services
// ---------------------------------------------------------------------------

func mcpServiceStatus(name string) interface{} {
	if runtime.GOOS == "darwin" {
		out, err := runCmd("launchctl", "list", name)
		if err != nil {
			// Try brew services
			out, err = runCmd("brew", "services", "info", name)
			if err != nil {
				return map[string]interface{}{"error": "service not found: " + name}
			}
		}
		return map[string]interface{}{"service": name, "status": out}
	}
	out, err := runCmd("systemctl", "status", name)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"service": name, "status": out}
}

func mcpServiceAction(name, action string) interface{} {
	switch action {
	case "start", "stop", "restart", "enable", "disable":
	default:
		return map[string]interface{}{"error": "action must be: start, stop, restart, enable, disable"}
	}
	if runtime.GOOS == "darwin" {
		out, err := runCmd("brew", "services", action, name)
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": out}
		}
		return map[string]interface{}{"ok": true, "service": name, "action": action}
	}
	out, err := runCmd("systemctl", action, name)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"ok": true, "service": name, "action": action}
}

func mcpServiceList() interface{} {
	if runtime.GOOS == "darwin" {
		out, err := runCmd("brew", "services", "list")
		if err != nil {
			out, _ = runCmd("launchctl", "list")
		}
		return map[string]interface{}{"services": out}
	}
	out, err := runCmd("systemctl", "list-units", "--type=service", "--no-pager")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"services": out}
}

// ---------------------------------------------------------------------------
// Benchmark tools
// ---------------------------------------------------------------------------

func mcpBenchmark(command, dir string) interface{} {
	if command == "" {
		command = detectBenchCommand(dir)
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := osexec.Command("sh", "-c", command)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func detectBenchCommand(dir string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go test -bench=. -benchmem ./... 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo bench 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "npm run bench 2>&1 || echo 'No bench script found'"
	}
	return "echo 'No benchmark framework detected. Specify a command.'"
}

// ---------------------------------------------------------------------------
// Diff tool
// ---------------------------------------------------------------------------

func mcpDiff(pathA, pathB string) interface{} {
	out, _ := runCmd("diff", "-u", pathA, pathB)
	if out == "" {
		return map[string]interface{}{"identical": true}
	}
	return map[string]interface{}{"identical": false, "diff": out}
}

// ---------------------------------------------------------------------------
// Environment tools
// ---------------------------------------------------------------------------

func mcpEnvList(filter string) interface{} {
	envs := os.Environ()
	var filtered []string
	for _, e := range envs {
		if filter == "" || strings.Contains(strings.ToUpper(e), strings.ToUpper(filter)) {
			// Mask sensitive values
			parts := strings.SplitN(e, "=", 2)
			key := parts[0]
			val := ""
			if len(parts) > 1 {
				val = parts[1]
			}
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "key") ||
				strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				if len(val) > 4 {
					val = val[:2] + "***" + val[len(val)-2:]
				} else {
					val = "***"
				}
			}
			filtered = append(filtered, key+"="+val)
		}
	}
	return map[string]interface{}{"variables": filtered, "count": len(filtered)}
}

func mcpEnvRead(path string) interface{} {
	if path == "" {
		path = ".env"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	// Parse and mask secrets
	lines := strings.Split(string(data), "\n")
	var masked []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			masked = append(masked, line)
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 {
			lower := strings.ToLower(parts[0])
			if strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "key") {
				val := parts[1]
				if len(val) > 4 {
					masked = append(masked, parts[0]+"="+val[:2]+"***"+val[len(val)-2:])
				} else {
					masked = append(masked, parts[0]+"=***")
				}
				continue
			}
		}
		masked = append(masked, line)
	}
	return map[string]interface{}{"path": path, "content": strings.Join(masked, "\n")}
}

// ---------------------------------------------------------------------------
// Crontab tool
// ---------------------------------------------------------------------------

func mcpCrontab(action, entry string) interface{} {
	switch action {
	case "list", "":
		out, err := runCmd("crontab", "-l")
		if err != nil {
			return map[string]interface{}{"entries": "(no crontab)"}
		}
		return map[string]interface{}{"entries": out}
	case "add":
		if entry == "" {
			return map[string]interface{}{"error": "entry required (e.g. '0 * * * * /usr/local/bin/yaver clean')"}
		}
		existing, _ := runCmd("crontab", "-l")
		newCrontab := strings.TrimSpace(existing) + "\n" + entry + "\n"
		cmd := osexec.Command("crontab", "-")
		cmd.Stdin = strings.NewReader(newCrontab)
		if err := cmd.Run(); err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "added": entry}
	default:
		return map[string]interface{}{"error": "action must be 'list' or 'add'"}
	}
}

// ---------------------------------------------------------------------------
// Cloud CLI tools
// ---------------------------------------------------------------------------

func mcpCloudCmd(provider string, args []string) interface{} {
	var binary string
	switch provider {
	case "aws":
		binary = "aws"
	case "gcloud", "gcp":
		binary = "gcloud"
	case "az", "azure":
		binary = "az"
	default:
		return map[string]interface{}{"error": "unsupported provider: " + provider + ". Use: aws, gcloud, az"}
	}
	cmd := osexec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("%s: %s — %s", binary, err, string(out))}
	}
	return map[string]interface{}{"output": strings.TrimSpace(string(out))}
}
