package main

import (
	"encoding/json"
	"net/http"
)

// handleGuestConfig handles GET/POST /guests/config.
// GET: returns all guest configs (cached from Convex + local project access).
// POST: updates guest config via Convex + optionally sets project access locally.
func (s *HTTPServer) handleGuestConfig(w http.ResponseWriter, r *http.Request) {
	if rejectGuestManagementCall(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleGuestConfigGet(w, r)
	case http.MethodPost:
		s.handleGuestConfigPost(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *HTTPServer) handleGuestConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.guestConfigMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "guest config not initialized")
		return
	}

	email := r.URL.Query().Get("email")

	configs := s.guestConfigMgr.GetAllConfigs()

	type configWithProjects struct {
		GuestUserID               string      `json:"guestUserId"`
		GuestEmail                string      `json:"guestEmail"`
		GuestName                 string      `json:"guestName"`
		DailyTokenLimit           *int        `json:"dailyTokenLimit,omitempty"`
		AllowedRunners            []string    `json:"allowedRunners,omitempty"`
		UsageMode                 string      `json:"usageMode,omitempty"`
		Schedule                  interface{} `json:"schedule,omitempty"`
		ShareAllDevices           *bool       `json:"shareAllDevices,omitempty"`
		DeviceIDs                 []string    `json:"deviceIds,omitempty"`
		ShareAllMachines          *bool       `json:"shareAllMachines,omitempty"`
		MachineIDs                []string    `json:"machineIds,omitempty"`
		ResourcePreset            string      `json:"resourcePreset,omitempty"`
		UseHostAPIKeys            *bool       `json:"useHostApiKeys,omitempty"`
		AllowGuestProvidedAPIKeys *bool       `json:"allowGuestProvidedApiKeys,omitempty"`
		AllowDesktopControl       *bool       `json:"allowDesktopControl,omitempty"`
		AllowBrowserControl       *bool       `json:"allowBrowserControl,omitempty"`
		AllowTunnelForward        *bool       `json:"allowTunnelForward,omitempty"`
		RequireIsolation          *bool       `json:"requireIsolation,omitempty"`
		CPULimitPercent           *int        `json:"cpuLimitPercent,omitempty"`
		RAMLimitMB                *int        `json:"ramLimitMb,omitempty"`
		PriorityMode              string      `json:"priorityMode,omitempty"`
		AllowedProjects           []string    `json:"allowedProjects,omitempty"`
	}

	var result []configWithProjects
	for _, c := range configs {
		if email != "" && c.GuestEmail != email {
			continue
		}
		result = append(result, configWithProjects{
			GuestUserID:               c.GuestUserID,
			GuestEmail:                c.GuestEmail,
			GuestName:                 c.GuestName,
			DailyTokenLimit:           c.DailyTokenLimit,
			AllowedRunners:            c.AllowedRunners,
			UsageMode:                 c.UsageMode,
			Schedule:                  c.Schedule,
			ShareAllDevices:           c.ShareAllDevices,
			DeviceIDs:                 c.DeviceIDs,
			ShareAllMachines:          c.ShareAllMachines,
			MachineIDs:                c.MachineIDs,
			ResourcePreset:            guestResourcePreset(&c),
			UseHostAPIKeys:            c.UseHostAPIKeys,
			AllowGuestProvidedAPIKeys: c.AllowGuestProvidedAPIKeys,
			AllowDesktopControl:       c.AllowDesktopControl,
			AllowBrowserControl:       c.AllowBrowserControl,
			AllowTunnelForward:        c.AllowTunnelForward,
			RequireIsolation:          c.RequireIsolation,
			CPULimitPercent:           c.CPULimitPercent,
			RAMLimitMB:                c.RAMLimitMB,
			PriorityMode:              c.PriorityMode,
			AllowedProjects:           s.guestConfigMgr.GetProjectAccess(c.GuestUserID),
		})
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"configs": result,
	})
}

func (s *HTTPServer) handleGuestConfigPost(w http.ResponseWriter, r *http.Request) {
	if s.guestConfigMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "guest config not initialized")
		return
	}

	var body struct {
		Email                     string   `json:"email"`
		DailyTokenLimit           *int     `json:"dailyTokenLimit,omitempty"`
		AllowedRunners            []string `json:"allowedRunners,omitempty"`
		UsageMode                 string   `json:"usageMode,omitempty"`
		ShareAllDevices           *bool    `json:"shareAllDevices,omitempty"`
		DeviceIDs                 []string `json:"deviceIds,omitempty"`
		ShareAllMachines          *bool    `json:"shareAllMachines,omitempty"`
		MachineIDs                []string `json:"machineIds,omitempty"`
		ResourcePreset            string   `json:"resourcePreset,omitempty"`
		UseHostAPIKeys            *bool    `json:"useHostApiKeys,omitempty"`
		AllowGuestProvidedAPIKeys *bool    `json:"allowGuestProvidedApiKeys,omitempty"`
		AllowDesktopControl       *bool    `json:"allowDesktopControl,omitempty"`
		AllowBrowserControl       *bool    `json:"allowBrowserControl,omitempty"`
		AllowTunnelForward        *bool    `json:"allowTunnelForward,omitempty"`
		RequireIsolation          *bool    `json:"requireIsolation,omitempty"`
		CPULimitPercent           *int     `json:"cpuLimitPercent,omitempty"`
		RAMLimitMB                *int     `json:"ramLimitMb,omitempty"`
		PriorityMode              string   `json:"priorityMode,omitempty"`
		Schedule                  *struct {
			StartHour int    `json:"startHour"`
			EndHour   int    `json:"endHour"`
			Timezone  string `json:"timezone,omitempty"`
		} `json:"schedule,omitempty"`
		AllowedProjects []string `json:"allowedProjects,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		jsonError(w, http.StatusBadRequest, "email is required")
		return
	}

	// Update Convex-side config (token limit, runners, usage mode, schedule)
	cfgPayload := map[string]interface{}{"email": body.Email}
	if body.DailyTokenLimit != nil {
		cfgPayload["dailyTokenLimit"] = *body.DailyTokenLimit
	}
	if body.AllowedRunners != nil {
		cfgPayload["allowedRunners"] = body.AllowedRunners
	}
	if body.UsageMode != "" {
		cfgPayload["usageMode"] = body.UsageMode
	}
	if body.ShareAllDevices != nil {
		cfgPayload["shareAllDevices"] = *body.ShareAllDevices
	}
	if body.DeviceIDs != nil {
		cfgPayload["deviceIds"] = body.DeviceIDs
	}
	if body.ShareAllMachines != nil {
		cfgPayload["shareAllMachines"] = *body.ShareAllMachines
	}
	if body.MachineIDs != nil {
		cfgPayload["machineIds"] = body.MachineIDs
	}
	if body.ResourcePreset != "" {
		cfgPayload["resourcePreset"] = body.ResourcePreset
	}
	if body.UseHostAPIKeys != nil {
		cfgPayload["useHostApiKeys"] = *body.UseHostAPIKeys
	}
	if body.AllowGuestProvidedAPIKeys != nil {
		cfgPayload["allowGuestProvidedApiKeys"] = *body.AllowGuestProvidedAPIKeys
	}
	if body.AllowDesktopControl != nil {
		cfgPayload["allowDesktopControl"] = *body.AllowDesktopControl
	}
	if body.AllowBrowserControl != nil {
		cfgPayload["allowBrowserControl"] = *body.AllowBrowserControl
	}
	if body.AllowTunnelForward != nil {
		cfgPayload["allowTunnelForward"] = *body.AllowTunnelForward
	}
	if body.RequireIsolation != nil {
		cfgPayload["requireIsolation"] = *body.RequireIsolation
	}
	if body.CPULimitPercent != nil {
		cfgPayload["cpuLimitPercent"] = *body.CPULimitPercent
	}
	if body.RAMLimitMB != nil {
		cfgPayload["ramLimitMb"] = *body.RAMLimitMB
	}
	if body.PriorityMode != "" {
		cfgPayload["priorityMode"] = body.PriorityMode
	}
	if body.Schedule != nil {
		cfgPayload["schedule"] = body.Schedule
	}

	// Only call Convex if there are Convex-side fields to update
	hasConvexFields := body.DailyTokenLimit != nil || body.AllowedRunners != nil ||
		body.UsageMode != "" || body.Schedule != nil ||
		body.ShareAllDevices != nil || body.DeviceIDs != nil ||
		body.ShareAllMachines != nil || body.MachineIDs != nil ||
		body.ResourcePreset != "" ||
		body.UseHostAPIKeys != nil || body.AllowGuestProvidedAPIKeys != nil ||
		body.AllowDesktopControl != nil || body.AllowBrowserControl != nil || body.AllowTunnelForward != nil ||
		body.RequireIsolation != nil ||
		body.CPULimitPercent != nil || body.RAMLimitMB != nil || body.PriorityMode != ""
	if hasConvexFields {
		if err := UpdateGuestConfig(s.convexURL, s.token, cfgPayload); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Update project access locally (P2P)
	if body.AllowedProjects != nil {
		// Find guestUserId from email
		configs := s.guestConfigMgr.GetAllConfigs()
		for _, c := range configs {
			if c.GuestEmail == body.Email {
				s.guestConfigMgr.SetProjectAccess(c.GuestUserID, body.AllowedProjects)
				break
			}
		}
	}

	// Refresh configs from Convex
	if hasConvexFields {
		if cfgs, err := FetchGuestConfigs(s.convexURL, s.token); err == nil {
			s.guestConfigMgr.UpdateConfigs(cfgs)
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Guest config updated for " + body.Email,
	})
}

// handleGuestUsage handles GET /guests/usage — returns daily usage stats.
func (s *HTTPServer) handleGuestUsage(w http.ResponseWriter, r *http.Request) {
	if rejectGuestManagementCall(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	date := r.URL.Query().Get("date")
	usage, err := FetchGuestUsage(s.convexURL, s.token, date)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch usage: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"usage": usage,
	})
}
