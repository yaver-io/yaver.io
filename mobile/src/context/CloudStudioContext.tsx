import AsyncStorage from "@react-native-async-storage/async-storage";
import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { getCloudStudioStatus, type CloudStudioStatus } from "../lib/auth";
import {
  quicClient,
  type GitRepository,
  type ProjectSession,
  type RunnerCapabilities,
} from "../lib/quic";
import { useAuth } from "./AuthContext";
import { useDevice } from "./DeviceContext";
import { ENABLE_CLOUD_WORKSPACE_UI } from "../lib/launchFlags";

const ACTIVE_SESSION_KEY = "@yaver/cloud_studio_active_project_session";

interface CloudStudioState {
  status: CloudStudioStatus | null;
  repositories: GitRepository[];
  projectSessions: ProjectSession[];
  activeProjectSession: ProjectSession | null;
  runnerCapabilities: RunnerCapabilities | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  createProjectSession: (repositoryId: string, baseRef?: string) => Promise<ProjectSession>;
  selectProjectSession: (session: ProjectSession) => Promise<void>;
  stopProjectSession: (projectSessionId: string) => Promise<void>;
}

const CloudStudioContext = createContext<CloudStudioState | undefined>(undefined);

export function CloudStudioProvider({ children }: { children: React.ReactNode }) {
  const { token } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();
  const [status, setStatus] = useState<CloudStudioStatus | null>(null);
  const [repositories, setRepositories] = useState<GitRepository[]>([]);
  const [projectSessions, setProjectSessions] = useState<ProjectSession[]>([]);
  const [activeProjectSession, setActiveProjectSession] = useState<ProjectSession | null>(null);
  const [runnerCapabilities, setRunnerCapabilities] = useState<RunnerCapabilities | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!ENABLE_CLOUD_WORKSPACE_UI || !token) {
      setStatus(null);
      setRepositories([]);
      setProjectSessions([]);
      setActiveProjectSession(null);
      setRunnerCapabilities(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const cloudStatus = await getCloudStudioStatus(token);
      setStatus(cloudStatus);

      if (connectionStatus !== "connected" || activeDevice?.deviceKind !== "cloud-runner") {
        setRepositories([]);
        setProjectSessions([]);
        setActiveProjectSession(null);
        setRunnerCapabilities(null);
        return;
      }

      // The established Ubuntu box predates the /v2 Cloud Studio protocol.
      // Keep it usable through its authenticated /projects, /tasks, and /dev
      // compatibility APIs while its registry/agent migration is pending.
      const legacyPrimary = !activeDevice.cloudWorkspaceId
        && activeDevice.name.trim().toLowerCase().replace(/\.local$/, "") === "ubuntu-4gb-hel1-1";
      if (legacyPrimary) {
        setRepositories([]);
        setProjectSessions([]);
        setActiveProjectSession(null);
        setRunnerCapabilities(null);
        return;
      }

      const [runnerRepositories, sessions, capabilities, storedSessionId] = await Promise.all([
        quicClient.listGitRepositories(),
        quicClient.listProjectSessions(),
        quicClient.getRunnerCapabilities(),
        AsyncStorage.getItem(ACTIVE_SESSION_KEY),
      ]);
      setRepositories(runnerRepositories);
      setProjectSessions(sessions);
      setRunnerCapabilities(capabilities);
      const selected = sessions.find((session) => session.status === "ready" && session.projectSessionId === storedSessionId)
        ?? sessions.find((session) => session.status === "ready")
        ?? null;
      setActiveProjectSession(selected);
      if (selected) await AsyncStorage.setItem(ACTIVE_SESSION_KEY, selected.projectSessionId);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Cloud Studio is unavailable");
    } finally {
      setLoading(false);
    }
  }, [token, connectionStatus, activeDevice?.id, activeDevice?.deviceKind]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const createProjectSession = useCallback(async (repositoryId: string, baseRef?: string) => {
    if (!ENABLE_CLOUD_WORKSPACE_UI) throw new Error("Hosted Cloud Workspace is unavailable");
    const session = await quicClient.createProjectSession(repositoryId, baseRef);
    setProjectSessions((current) => [session, ...current.filter((item) => item.projectSessionId !== session.projectSessionId)]);
    setActiveProjectSession(session);
    await AsyncStorage.setItem(ACTIVE_SESSION_KEY, session.projectSessionId);
    return session;
  }, []);

  const selectProjectSession = useCallback(async (session: ProjectSession) => {
    if (!ENABLE_CLOUD_WORKSPACE_UI) throw new Error("Hosted Cloud Workspace is unavailable");
    if (session.status !== "ready") throw new Error("Project session is not ready");
    setActiveProjectSession(session);
    await AsyncStorage.setItem(ACTIVE_SESSION_KEY, session.projectSessionId);
  }, []);

  const stopProjectSession = useCallback(async (projectSessionId: string) => {
    if (!ENABLE_CLOUD_WORKSPACE_UI) throw new Error("Hosted Cloud Workspace is unavailable");
    const stopped = await quicClient.stopProjectSession(projectSessionId);
    setProjectSessions((current) => current.map((item) => item.projectSessionId === projectSessionId ? stopped : item));
    setActiveProjectSession((current) => current?.projectSessionId === projectSessionId ? null : current);
    const stored = await AsyncStorage.getItem(ACTIVE_SESSION_KEY);
    if (stored === projectSessionId) await AsyncStorage.removeItem(ACTIVE_SESSION_KEY);
  }, []);

  const value = useMemo<CloudStudioState>(() => ({
    status,
    repositories,
    projectSessions,
    activeProjectSession,
    runnerCapabilities,
    loading,
    error,
    refresh,
    createProjectSession,
    selectProjectSession,
    stopProjectSession,
  }), [
    status, repositories, projectSessions, activeProjectSession,
    runnerCapabilities, loading, error, refresh,
    createProjectSession, selectProjectSession, stopProjectSession,
  ]);

  return <CloudStudioContext.Provider value={value}>{children}</CloudStudioContext.Provider>;
}

export function useCloudStudio(): CloudStudioState {
  const context = useContext(CloudStudioContext);
  if (!context) throw new Error("useCloudStudio must be used within CloudStudioProvider");
  return context;
}
