package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// handleWorkspaceMCPTool handles MCP tool calls for workspace features.
// Returns nil if the tool name is not a workspace tool (caller should continue to default case).
func (s *HTTPServer) handleWorkspaceMCPTool(call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}) interface{} {
	// Parse arguments into a generic map for workspace tools
	var args map[string]interface{}
	if len(call.Arguments) > 0 {
		_ = json.Unmarshal(call.Arguments, &args)
	}
	if args == nil {
		args = make(map[string]interface{})
	}
	// Alias for readability in the switch cases
	_ = args
	switch call.Name {

	// --- Services ---
	case "services_start":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		var names []string
		if arr, ok := args["names"].([]interface{}); ok {
			for _, n := range arr {
				if str, ok := n.(string); ok {
					names = append(names, str)
				}
			}
		}
		result, err := s.servicesMgr.Start(names...)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "services_stop":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		var names []string
		if arr, ok := args["names"].([]interface{}); ok {
			for _, n := range arr {
				if str, ok := n.(string); ok {
					names = append(names, str)
				}
			}
		}
		result, err := s.servicesMgr.Stop(names...)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "services_status":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		statuses, err := s.servicesMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(statuses)

	case "services_logs":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		lines := 50
		if l, ok := args["lines"].(float64); ok {
			lines = int(l)
		}
		result, err := s.servicesMgr.Logs(name, lines)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "services_add":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		result, err := s.servicesMgr.Add(name, nil)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "services_remove":
		if s.servicesMgr == nil {
			s.servicesMgr = NewServicesManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		result, err := s.servicesMgr.Remove(name)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Proxy ---
	case "proxy_start":
		if s.proxyMgr == nil {
			s.proxyMgr = NewProxyManager()
		}
		result, err := s.proxyMgr.Start()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "proxy_stop":
		if s.proxyMgr == nil {
			return mcpToolError("proxy is not running")
		}
		result, err := s.proxyMgr.Stop()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "proxy_add":
		if s.proxyMgr == nil {
			s.proxyMgr = NewProxyManager()
		}
		domain, _ := args["domain"].(string)
		target, _ := args["target"].(string)
		useTLS := true
		if t, ok := args["tls"].(bool); ok {
			useTLS = t
		}
		result, err := s.proxyMgr.Add(domain, target, useTLS)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "proxy_remove":
		if s.proxyMgr == nil {
			return mcpToolError("proxy is not configured")
		}
		domain, _ := args["domain"].(string)
		result, err := s.proxyMgr.Remove(domain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "proxy_list":
		if s.proxyMgr == nil {
			s.proxyMgr = NewProxyManager()
		}
		routes, err := s.proxyMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(routes)

	case "proxy_status":
		if s.proxyMgr == nil {
			s.proxyMgr = NewProxyManager()
		}
		status, err := s.proxyMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	// --- DNS ---
	case "dns_add":
		if s.dnsMgr == nil {
			s.dnsMgr = NewDNSManager()
		}
		hostname, _ := args["hostname"].(string)
		ip, _ := args["ip"].(string)
		result, err := s.dnsMgr.Add(hostname, ip)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "dns_remove":
		if s.dnsMgr == nil {
			s.dnsMgr = NewDNSManager()
		}
		hostname, _ := args["hostname"].(string)
		result, err := s.dnsMgr.Remove(hostname)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "dns_list":
		if s.dnsMgr == nil {
			s.dnsMgr = NewDNSManager()
		}
		entries, err := s.dnsMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(entries)

	case "dns_flush":
		if s.dnsMgr == nil {
			s.dnsMgr = NewDNSManager()
		}
		result, err := s.dnsMgr.Flush()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Storage (MinIO) ---
	case "storage_start":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.storageMgr.Start(port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "storage_stop":
		if s.storageMgr == nil {
			return mcpToolError("storage is not running")
		}
		result, err := s.storageMgr.Stop()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "storage_status":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		status, err := s.storageMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	case "storage_bucket_create":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		name, _ := args["name"].(string)
		result, err := s.storageMgr.CreateBucket(name)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "storage_bucket_list":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		buckets, err := s.storageMgr.ListBuckets()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(buckets)

	case "storage_upload":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		bucket, _ := args["bucket"].(string)
		key, _ := args["key"].(string)
		file, _ := args["file"].(string)
		result, err := s.storageMgr.Upload(bucket, key, file)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "storage_list":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		bucket, _ := args["bucket"].(string)
		prefix, _ := args["prefix"].(string)
		objects, err := s.storageMgr.ListObjects(bucket, prefix)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(objects)

	case "storage_presign":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		bucket, _ := args["bucket"].(string)
		key, _ := args["key"].(string)
		expiry := 24 * time.Hour
		if e, ok := args["expiry"].(string); ok {
			if d, err := time.ParseDuration(e); err == nil {
				expiry = d
			}
		}
		result, err := s.storageMgr.Presign(bucket, key, expiry)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "storage_config":
		if s.storageMgr == nil {
			s.storageMgr = NewStorageManager()
		}
		return mcpToolJSON(s.storageMgr.Config())

	// --- Mock Server ---
	case "mock_start":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.mockServer.Start(port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_stop":
		if s.mockServer == nil {
			return mcpToolError("mock server is not running")
		}
		result, err := s.mockServer.Stop()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_add":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		method, _ := args["method"].(string)
		path, _ := args["path"].(string)
		status := 200
		if st, ok := args["status"].(float64); ok {
			status = int(st)
		}
		body, _ := args["body"].(string)
		var headers map[string]string
		if h, ok := args["headers"].(map[string]interface{}); ok {
			headers = make(map[string]string)
			for k, v := range h {
				headers[k] = fmt.Sprintf("%v", v)
			}
		}
		result, err := s.mockServer.AddRoute(method, path, status, body, headers)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_list":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		routes, err := s.mockServer.ListRoutes()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(routes)

	case "mock_reset":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		result, err := s.mockServer.Reset()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_preset":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		name, _ := args["name"].(string)
		result, err := s.mockServer.LoadPreset(name)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_record":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		result, err := s.mockServer.StartRecording()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "mock_openapi":
		if s.mockServer == nil {
			s.mockServer = NewMockServer()
		}
		specPath, _ := args["spec_path"].(string)
		result, err := s.mockServer.LoadOpenAPI(specPath)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Pre-deployment Checks ---
	case "check_run":
		if s.preCheckMgr == nil {
			s.preCheckMgr = NewPreCheckManager(s.taskMgr.workDir)
		}
		config := &CheckConfig{WorkDir: s.taskMgr.workDir}
		if fix, ok := args["fix"].(bool); ok {
			config.Fix = fix
		}
		if skip, ok := args["skip"].([]interface{}); ok {
			for _, sk := range skip {
				if str, ok := sk.(string); ok {
					config.Skip = append(config.Skip, str)
				}
			}
		}
		report, err := s.preCheckMgr.Run(config)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(s.preCheckMgr.FormatReport(report))

	case "check_single":
		if s.preCheckMgr == nil {
			s.preCheckMgr = NewPreCheckManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		fix, _ := args["fix"].(bool)
		result, err := s.preCheckMgr.RunSingle(name, fix)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	// --- Performance Testing ---
	case "perf_lighthouse":
		if s.perfMgr == nil {
			s.perfMgr = NewPerfManager(s.taskMgr.workDir)
		}
		url, _ := args["url"].(string)
		device, _ := args["device"].(string)
		result, err := s.perfMgr.Lighthouse(url, device)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(s.perfMgr.FormatLighthouse(result))

	case "perf_loadtest":
		if s.perfMgr == nil {
			s.perfMgr = NewPerfManager(s.taskMgr.workDir)
		}
		url, _ := args["url"].(string)
		requests := 1000
		if r, ok := args["requests"].(float64); ok {
			requests = int(r)
		}
		concurrency := 10
		if c, ok := args["concurrency"].(float64); ok {
			concurrency = int(c)
		}
		duration := time.Duration(0)
		if d, ok := args["duration"].(string); ok {
			if parsed, err := time.ParseDuration(d); err == nil {
				duration = parsed
			}
		}
		result, err := s.perfMgr.LoadTest(url, requests, concurrency, duration)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(s.perfMgr.FormatLoadTest(result))

	case "perf_compare":
		if s.perfMgr == nil {
			s.perfMgr = NewPerfManager(s.taskMgr.workDir)
		}
		url, _ := args["url"].(string)
		result, err := s.perfMgr.Compare(url)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	// --- Database Lifecycle ---
	case "db_migrate":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		target, _ := args["target"].(string)
		if target == "" {
			target = "local"
		}
		result, err := s.dbLifecycleMgr.Migrate(target)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_generate":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		result, err := s.dbLifecycleMgr.Generate(name)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_push":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		result, err := s.dbLifecycleMgr.Push()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_seed":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		result, err := s.dbLifecycleMgr.Seed()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_reset":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		force, _ := args["force"].(bool)
		result, err := s.dbLifecycleMgr.Reset(force)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_studio":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.dbLifecycleMgr.Studio(port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_backup":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		dbURL, _ := args["db_url"].(string)
		result, err := s.dbLifecycleMgr.Backup(dbURL)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	case "db_restore":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		backupPath, _ := args["backup_path"].(string)
		dbURL, _ := args["db_url"].(string)
		result, err := s.dbLifecycleMgr.Restore(backupPath, dbURL)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "db_status":
		if s.dbLifecycleMgr == nil {
			s.dbLifecycleMgr = NewDBLifecycleManager(s.taskMgr.workDir)
		}
		status, err := s.dbLifecycleMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	// --- Preview Environments ---
	case "preview_create":
		if s.previewMgr == nil {
			s.previewMgr = NewPreviewManager(s.taskMgr.workDir)
		}
		branch, _ := args["branch"].(string)
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		expose, _ := args["expose"].(bool)
		result, err := s.previewMgr.Create(branch, port, expose)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	case "preview_list":
		if s.previewMgr == nil {
			s.previewMgr = NewPreviewManager(s.taskMgr.workDir)
		}
		list, err := s.previewMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(list)

	case "preview_stop":
		if s.previewMgr == nil {
			return mcpToolError("no previews running")
		}
		branch, _ := args["branch"].(string)
		result, err := s.previewMgr.Stop(branch)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "preview_stop_all":
		if s.previewMgr == nil {
			return mcpToolResult("no previews running")
		}
		result, err := s.previewMgr.StopAll()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- OAuth Wizard ---
	case "auth_oauth_setup":
		if s.oauthWizardMgr == nil {
			s.oauthWizardMgr = NewOAuthWizardManager(s.taskMgr.workDir)
		}
		provider, _ := args["provider"].(string)
		steps, err := s.oauthWizardMgr.Setup(provider)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(steps)

	case "auth_oauth_save":
		if s.oauthWizardMgr == nil {
			s.oauthWizardMgr = NewOAuthWizardManager(s.taskMgr.workDir)
		}
		provider, _ := args["provider"].(string)
		creds := make(map[string]string)
		if c, ok := args["credentials"].(map[string]interface{}); ok {
			for k, v := range c {
				creds[k] = fmt.Sprintf("%v", v)
			}
		}
		result, err := s.oauthWizardMgr.SaveCredentials(provider, creds)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "auth_oauth_test":
		if s.oauthWizardMgr == nil {
			s.oauthWizardMgr = NewOAuthWizardManager(s.taskMgr.workDir)
		}
		provider, _ := args["provider"].(string)
		result, err := s.oauthWizardMgr.Test(provider)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "auth_oauth_list":
		if s.oauthWizardMgr == nil {
			s.oauthWizardMgr = NewOAuthWizardManager(s.taskMgr.workDir)
		}
		statuses, err := s.oauthWizardMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(statuses)

	case "auth_oauth_migrate":
		if s.oauthWizardMgr == nil {
			s.oauthWizardMgr = NewOAuthWizardManager(s.taskMgr.workDir)
		}
		oldDomain, _ := args["old_domain"].(string)
		newDomain, _ := args["new_domain"].(string)
		result, err := s.oauthWizardMgr.MigrateURIs(oldDomain, newDomain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Cloud Deploy ---
	case "cloud_deploy":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		plan, _ := args["plan"].(string)
		region, _ := args["region"].(string)
		name, _ := args["name"].(string)
		domain, _ := args["domain"].(string)
		result, err := s.cloudDeployMgr.Deploy(plan, region, name, domain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "cloud_status":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		status, err := s.cloudDeployMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	case "cloud_logs":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		app, _ := args["app"].(string)
		lines := 100
		if l, ok := args["lines"].(float64); ok {
			lines = int(l)
		}
		result, err := s.cloudDeployMgr.Logs(app, lines)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "cloud_redeploy":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		err := s.cloudDeployMgr.Redeploy()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Redeployed successfully.")

	case "cloud_scale":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		plan, _ := args["plan"].(string)
		result, err := s.cloudDeployMgr.Scale(plan)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "cloud_backup":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		err := s.cloudDeployMgr.Backup()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Backup completed successfully.")

	case "cloud_destroy":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		confirm, _ := args["confirm"].(bool)
		err := s.cloudDeployMgr.Destroy(confirm)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Deployment destroyed.")

	case "cloud_plans":
		if s.cloudDeployMgr == nil {
			s.cloudDeployMgr, _ = NewCloudDeployManager(s.taskMgr.workDir)
		}
		return mcpToolJSON(s.cloudDeployMgr.ListPlans())

	// --- Migration ---
	case "migrate_plan":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		plan, err := s.migrateMgr.Plan(from, to)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(plan)

	case "migrate_run":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		planID, _ := args["plan_id"].(string)
		step := 0
		if st, ok := args["step"].(float64); ok {
			step = int(st)
		}
		result, err := s.migrateMgr.Run(planID, step)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "migrate_status":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		plan, err := s.migrateMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(plan)

	case "migrate_rollback":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		planID, _ := args["plan_id"].(string)
		step := 0
		if st, ok := args["step"].(float64); ok {
			step = int(st)
		}
		result, err := s.migrateMgr.Rollback(planID, step)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "migrate_targets":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		return mcpToolJSON(s.migrateMgr.ListTargets())

	case "migrate_verify":
		if s.migrateMgr == nil {
			s.migrateMgr = NewMigrateManager(s.taskMgr.workDir)
		}
		planID, _ := args["plan_id"].(string)
		result, err := s.migrateMgr.Verify(planID)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Remote Machine ---
	case "remote_setup":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		host, _ := args["host"].(string)
		user, _ := args["user"].(string)
		if user == "" {
			user = "root"
		}
		result, err := s.remoteMgr.Setup(host, user)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "remote_status":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		machines, err := s.remoteMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(machines)

	case "remote_provision":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		provider, _ := args["provider"].(string)
		size, _ := args["size"].(string)
		region, _ := args["region"].(string)
		result, err := s.remoteMgr.Provision(provider, size, region)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "remote_destroy":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		machineID, _ := args["machine_id"].(string)
		confirm, _ := args["confirm"].(bool)
		result, err := s.remoteMgr.Destroy(machineID, confirm)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "remote_cost":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		result, err := s.remoteMgr.Cost()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "remote_exec":
		if s.remoteMgr == nil {
			s.remoteMgr = NewRemoteManager()
		}
		machineID, _ := args["machine_id"].(string)
		command, _ := args["command"].(string)
		result, err := s.remoteMgr.Exec(machineID, command)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Scale ---
	case "scale_check":
		if s.scaleMgr == nil {
			s.scaleMgr = NewScaleManager(s.taskMgr.workDir)
		}
		status, err := s.scaleMgr.Check()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(s.scaleMgr.FormatStatus(status))

	case "scale_plan":
		if s.scaleMgr == nil {
			s.scaleMgr = NewScaleManager(s.taskMgr.workDir)
		}
		plan, _ := args["plan"].(string)
		result, err := s.scaleMgr.Plan(plan)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "scale_cdn":
		if s.scaleMgr == nil {
			s.scaleMgr = NewScaleManager(s.taskMgr.workDir)
		}
		domain, _ := args["domain"].(string)
		provider, _ := args["provider"].(string)
		result, err := s.scaleMgr.CDN(domain, provider)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "scale_cache":
		if s.scaleMgr == nil {
			s.scaleMgr = NewScaleManager(s.taskMgr.workDir)
		}
		backend, _ := args["backend"].(string)
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.scaleMgr.Cache(backend, port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "scale_optimize":
		if s.scaleMgr == nil {
			s.scaleMgr = NewScaleManager(s.taskMgr.workDir)
		}
		result, err := s.scaleMgr.Optimize()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- PocketBase Backend ---
	case "backend_start":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		mode, _ := args["mode"].(string)
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.pocketBaseMgr.Start(mode, port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "backend_stop":
		if s.pocketBaseMgr == nil {
			return mcpToolError("PocketBase is not running")
		}
		result, err := s.pocketBaseMgr.Stop()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "backend_status":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		status, err := s.pocketBaseMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	case "backend_collections":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		collections, err := s.pocketBaseMgr.Collections()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(collections)

	case "backend_collection_create":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		name, _ := args["name"].(string)
		collType, _ := args["type"].(string)
		if collType == "" {
			collType = "base"
		}
		var schema []PBField
		if schemaRaw, ok := args["schema"]; ok {
			raw, _ := json.Marshal(schemaRaw)
			_ = json.Unmarshal(raw, &schema)
		}
		result, err := s.pocketBaseMgr.CreateCollection(name, collType, schema)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "backend_records":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		collection, _ := args["collection"].(string)
		filter, _ := args["filter"].(string)
		sort, _ := args["sort"].(string)
		limit := 20
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}
		records, err := s.pocketBaseMgr.Records(collection, filter, sort, limit)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(records)

	case "backend_users":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		action, _ := args["action"].(string)
		email, _ := args["email"].(string)
		password, _ := args["password"].(string)
		result, err := s.pocketBaseMgr.Users(action, email, password)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	case "backend_backup":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		output, _ := args["output"].(string)
		result, err := s.pocketBaseMgr.Backup(output)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	case "backend_setup":
		if s.pocketBaseMgr == nil {
			s.pocketBaseMgr = NewPocketBaseManager()
		}
		framework, _ := args["framework"].(string)
		result, err := s.pocketBaseMgr.Setup(framework)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Platform ---
	case "platform_init":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		mode, _ := args["mode"].(string)
		domain, _ := args["domain"].(string)
		result, err := s.platformMgr.Init(mode, domain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "platform_deploy":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		directory, _ := args["directory"].(string)
		name, _ := args["name"].(string)
		domain, _ := args["domain"].(string)
		result, err := s.platformMgr.Deploy(directory, name, domain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "platform_redeploy":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		app, _ := args["app"].(string)
		result, err := s.platformMgr.Redeploy(app)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "platform_apps":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		apps, err := s.platformMgr.Apps()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(apps)

	case "platform_logs":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		app, _ := args["app"].(string)
		lines := 100
		if l, ok := args["lines"].(float64); ok {
			lines = int(l)
		}
		result, err := s.platformMgr.AppLogs(app, lines)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "platform_remove":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		app, _ := args["app"].(string)
		result, err := s.platformMgr.Remove(app)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "platform_status":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		status, err := s.platformMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	case "platform_preview":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		branch, _ := args["branch"].(string)
		app, _ := args["app"].(string)
		result, err := s.platformMgr.Preview(branch, app)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	case "platform_webhook":
		if s.platformMgr == nil {
			s.platformMgr = NewPlatformManager(s.taskMgr.workDir)
		}
		repo, _ := args["repo"].(string)
		branch, _ := args["branch"].(string)
		app, _ := args["app"].(string)
		result, err := s.platformMgr.WebhookSetup(repo, branch, app)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(result)

	// --- Domain/DNS/HTTPS ---
	case "domain_setup":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domain, _ := args["domain"].(string)
		provider, _ := args["provider"].(string)
		result, err := s.domainMgr.Setup(domain, provider)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "domain_add":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domain, _ := args["domain"].(string)
		target, _ := args["target"].(string)
		path, _ := args["path"].(string)
		result, err := s.domainMgr.Add(domain, target, path)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "domain_list":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domains, err := s.domainMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(domains)

	case "domain_ssl_status":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domains, err := s.domainMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(domains)

	case "domain_dns_check":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domain, _ := args["domain"].(string)
		result, err := s.domainMgr.DNSCheck(domain)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "domain_detect_ip":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		ip, ipType, err := s.domainMgr.DetectIP()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(map[string]string{"ip": ip, "type": ipType})

	case "domain_ddns_start":
		if s.domainMgr == nil {
			s.domainMgr = NewDomainManager()
		}
		domain, _ := args["domain"].(string)
		provider, _ := args["provider"].(string)
		interval := 5 * time.Minute
		if iv, ok := args["interval"].(string); ok {
			if d, err := time.ParseDuration(iv); err == nil {
				interval = d
			}
		}
		result, err := s.domainMgr.DDNSStart(provider, domain, interval)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- Site Generator ---
	case "site_create":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		siteType, _ := args["type"].(string)
		framework, _ := args["framework"].(string)
		result, err := s.siteMgr.Create(siteType, name, framework)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_generate":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		productName, _ := args["product_name"].(string)
		tagline, _ := args["tagline"].(string)
		cta, _ := args["cta"].(string)
		style, _ := args["style"].(string)
		var features []string
		if f, ok := args["features"].([]interface{}); ok {
			for _, feat := range f {
				if str, ok := feat.(string); ok {
					features = append(features, str)
				}
			}
		}
		result, err := s.siteMgr.Generate(productName, tagline, features, cta, style)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_build":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		result, err := s.siteMgr.Build()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_serve":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.siteMgr.Serve(port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_deploy":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		result, err := s.siteMgr.Deploy()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_pages":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		pages, err := s.siteMgr.Pages()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(pages)

	case "site_page_add":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		slug, _ := args["slug"].(string)
		title, _ := args["title"].(string)
		content, _ := args["content"].(string)
		result, err := s.siteMgr.PageAdd(slug, title, content)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_blog_post":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		title, _ := args["title"].(string)
		content, _ := args["content"].(string)
		draft, _ := args["draft"].(bool)
		var tags []string
		if t, ok := args["tags"].([]interface{}); ok {
			for _, tag := range t {
				if str, ok := tag.(string); ok {
					tags = append(tags, str)
				}
			}
		}
		result, err := s.siteMgr.BlogPost(title, content, tags, draft)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "site_blog_list":
		if s.siteMgr == nil {
			s.siteMgr = NewSiteManager(s.taskMgr.workDir)
		}
		posts, err := s.siteMgr.BlogList()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(posts)

	// --- Form Handler ---
	case "form_create":
		if s.formMgr == nil {
			s.formMgr = NewFormManager()
		}
		var config FormConfig
		raw, _ := json.Marshal(args)
		_ = json.Unmarshal(raw, &config)
		result, err := s.formMgr.Create(&config)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "form_list":
		if s.formMgr == nil {
			s.formMgr = NewFormManager()
		}
		forms, err := s.formMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(forms)

	case "form_submissions":
		if s.formMgr == nil {
			s.formMgr = NewFormManager()
		}
		formName, _ := args["form"].(string)
		subs, err := s.formMgr.Submissions(formName, 0)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(subs)

	case "form_export":
		if s.formMgr == nil {
			s.formMgr = NewFormManager()
		}
		formName, _ := args["form"].(string)
		result, err := s.formMgr.Export(formName)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "form_component":
		if s.formMgr == nil {
			s.formMgr = NewFormManager()
		}
		formName, _ := args["form"].(string)
		framework, _ := args["framework"].(string)
		result, err := s.formMgr.GenerateComponent(formName, framework)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- SEO ---
	case "seo_audit":
		if s.seoMgr == nil {
			s.seoMgr = NewSEOManager(s.taskMgr.workDir)
		}
		report, err := s.seoMgr.Audit()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(report)

	case "seo_fix":
		if s.seoMgr == nil {
			s.seoMgr = NewSEOManager(s.taskMgr.workDir)
		}
		what, _ := args["fix"].(string)
		if what == "" {
			what = "all"
		}
		result, err := s.seoMgr.Fix(what)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "seo_report":
		if s.seoMgr == nil {
			s.seoMgr = NewSEOManager(s.taskMgr.workDir)
		}
		result, err := s.seoMgr.Report()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "seo_sitemap":
		if s.seoMgr == nil {
			s.seoMgr = NewSEOManager(s.taskMgr.workDir)
		}
		result, err := s.seoMgr.Sitemap()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "seo_schema":
		if s.seoMgr == nil {
			s.seoMgr = NewSEOManager(s.taskMgr.workDir)
		}
		page, _ := args["page"].(string)
		schemaType, _ := args["type"].(string)
		result, err := s.seoMgr.Schema(page, schemaType)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	// --- CMS ---
	case "cms_start":
		if s.cmsMgr == nil {
			s.cmsMgr = NewCMSManager(s.taskMgr.workDir)
		}
		engine, _ := args["engine"].(string)
		port := 0
		if p, ok := args["port"].(float64); ok {
			port = int(p)
		}
		result, err := s.cmsMgr.Start(engine, port)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "cms_stop":
		if s.cmsMgr == nil {
			return mcpToolError("CMS is not running")
		}
		result, err := s.cmsMgr.Stop()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	case "cms_status":
		if s.cmsMgr == nil {
			s.cmsMgr = NewCMSManager(s.taskMgr.workDir)
		}
		status, err := s.cmsMgr.Status()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(status)

	case "cms_content":
		if s.cmsMgr == nil {
			s.cmsMgr = NewCMSManager(s.taskMgr.workDir)
		}
		collection, _ := args["collection"].(string)
		content, err := s.cmsMgr.Content(collection)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(content)

	// --- Templates ---
	case "template_list":
		if s.templateMgr == nil {
			s.templateMgr = NewTemplateManager(s.taskMgr.workDir)
		}
		templates, err := s.templateMgr.List()
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(templates)

	case "template_use":
		if s.templateMgr == nil {
			s.templateMgr = NewTemplateManager(s.taskMgr.workDir)
		}
		name, _ := args["name"].(string)
		projectName, _ := args["project_name"].(string)
		result, err := s.templateMgr.Use(name, projectName)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(result)

	default:
		return nil // not a workspace tool
	}
}
