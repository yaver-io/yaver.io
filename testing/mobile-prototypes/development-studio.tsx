import React, { useState, useEffect } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, ScrollView, ActivityIndicator, Alert } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useAgentClient } from '../lib/agentClient';
import { Device } from '../types';

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#0a0a0f',
  },
  header: {
    padding: 20,
    borderBottomWidth: 1,
    borderBottomColor: '#1a1a2e',
  },
  headerTitle: {
    fontSize: 24,
    fontWeight: 'bold',
    color: '#ffffff',
    marginBottom: 8,
  },
  headerSubtitle: {
    fontSize: 14,
    color: '#888899',
  },
  projectsList: {
    padding: 16,
  },
  projectCard: {
    backgroundColor: '#1a1a2e',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: '#2a2a4e',
  },
  projectHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 12,
  },
  projectName: {
    fontSize: 18,
    fontWeight: 'bold',
    color: '#ffffff',
  },
  projectStatus: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 12,
    fontSize: 12,
    fontWeight: 'bold',
  },
  statusIdle: {
    backgroundColor: '#2a2a4e',
    color: '#888899',
  },
  statusBuilding: {
    backgroundColor: '#2a1f00',
    color: '#ffaa00',
  },
  statusDeployed: {
    backgroundColor: '#002a00',
    color: '#00ff00',
  },
  statusTesting: {
    backgroundColor: '#002a2a',
    color: '#00ffff',
  },
  statusFailed: {
    backgroundColor: '#2a0000',
    color: '#ff0000',
  },
  projectInfo: {
    marginBottom: 12,
  },
  infoRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginBottom: 4,
  },
  infoLabel: {
    fontSize: 12,
    color: '#888899',
  },
  infoValue: {
    fontSize: 12,
    color: '#ffffff',
  },
  projectActions: {
    flexDirection: 'row',
    gap: 8,
    marginTop: 8,
  },
  actionButton: {
    flex: 1,
    paddingVertical: 10,
    paddingHorizontal: 16,
    borderRadius: 8,
    alignItems: 'center',
  },
  actionButtonPrimary: {
    backgroundColor: '#6366f1',
  },
  actionButtonSecondary: {
    backgroundColor: '#2a2a4e',
  },
  actionButtonDanger: {
    backgroundColor: '#ef4444',
  },
  actionButtonText: {
    color: '#ffffff',
    fontSize: 12,
    fontWeight: 'bold',
  },
  workflowSection: {
    padding: 16,
    borderTopWidth: 1,
    borderTopColor: '#1a1a2e',
  },
  workflowTitle: {
    fontSize: 18,
    fontWeight: 'bold',
    color: '#ffffff',
    marginBottom: 16,
  },
  workflowOptions: {
    marginBottom: 16,
  },
  workflowOption: {
    backgroundColor: '#1a1a2e',
    borderRadius: 8,
    padding: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: '#2a2a4e',
  },
  workflowOptionSelected: {
    borderColor: '#6366f1',
    backgroundColor: '#1a1a3e',
  },
  workflowOptionTitle: {
    fontSize: 14,
    fontWeight: 'bold',
    color: '#ffffff',
    marginBottom: 4,
  },
  workflowOptionDescription: {
    fontSize: 12,
    color: '#888899',
  },
  workflowButton: {
    backgroundColor: '#6366f1',
    paddingVertical: 14,
    borderRadius: 8,
    alignItems: 'center',
  },
  workflowButtonText: {
    color: '#ffffff',
    fontSize: 16,
    fontWeight: 'bold',
  },
  agentSelector: {
    padding: 16,
    borderTopWidth: 1,
    borderTopColor: '#1a1a2e',
  },
  agentSelectorTitle: {
    fontSize: 16,
    fontWeight: 'bold',
    color: '#ffffff',
    marginBottom: 12,
  },
  agentButtons: {
    flexDirection: 'row',
    gap: 8,
  },
  agentButton: {
    flex: 1,
    paddingVertical: 10,
    borderRadius: 8,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#2a2a4e',
    backgroundColor: '#1a1a2e',
  },
  agentButtonSelected: {
    borderColor: '#6366f1',
    backgroundColor: '#1a1a3e',
  },
  agentButtonText: {
    color: '#ffffff',
    fontSize: 12,
    fontWeight: 'bold',
  },
  tasksList: {
    padding: 16,
  },
  taskCard: {
    backgroundColor: '#1a1a2e',
    borderRadius: 8,
    padding: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: '#2a2a4e',
  },
  taskHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 8,
  },
  taskId: {
    fontSize: 12,
    color: '#888899',
  },
  taskStatus: {
    fontSize: 12,
    fontWeight: 'bold',
  },
  taskProject: {
    fontSize: 14,
    fontWeight: 'bold',
    color: '#ffffff',
    marginBottom: 4,
  },
  taskOutput: {
    fontSize: 12,
    color: '#888899',
    fontFamily: 'monospace',
  },
  loadingOverlay: {
    position: 'absolute',
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    backgroundColor: 'rgba(0, 0, 0, 0.7)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  loadingText: {
    color: '#ffffff',
    marginTop: 16,
    fontSize: 16,
  },
});

type ProjectStatus = {
  path: string;
  status: string;
  environment: string;
  port: number;
  last_deploy: string;
  hetzner_id: string;
};

type ActiveTask = {
  id: string;
  project_name: string;
  type: string;
  status: string;
  start_time: string;
  end_time: string;
  output: string;
  error: string;
};

interface DevelopmentStudioProps {
  device: Device;
}

const DevelopmentStudio: React.FC<DevelopmentStudioProps> = ({ device }) => {
  const agentClient = useAgentClient();
  const [projects, setProjects] = useState<Record<string, ProjectStatus>>({});
  const [activeTasks, setActiveTasks] = useState<ActiveTask[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string>('opencode');
  const [selectedWorkflow, setSelectedWorkflow] = useState<string>('full');
  const [loading, setLoading] = useState<boolean>(false);
  const [loadingMessage, setLoadingMessage] = useState<string>('');

  // Load project status on mount
  useEffect(() => {
    loadProjectStatus();
    const interval = setInterval(loadProjectStatus, 5000); // Refresh every 5 seconds
    return () => clearInterval(interval);
  }, [device.deviceId]);

  // Load active tasks periodically
  useEffect(() => {
    loadActiveTasks();
    const interval = setInterval(loadActiveTasks, 3000); // Refresh every 3 seconds
    return () => clearInterval(interval);
  }, [device.deviceId]);

  const loadProjectStatus = async () => {
    try {
      const response = await fetch(`http://${device.ipAddress}:18080/projects/list`);
      const data = await response.json();
      setProjects(data.projects || {});
    } catch (error) {
      console.error('Failed to load project status:', error);
    }
  };

  const loadActiveTasks = async () => {
    try {
      const response = await fetch(`http://${device.ipAddress}:18080/tasks/list`);
      const data = await response.json();
      setActiveTasks(data.tasks || []);
    } catch (error) {
      console.error('Failed to load active tasks:', error);
    }
  };

  const deployProject = async (projectName: string) => {
    try {
      setLoading(true);
      setLoadingMessage(`Deploying ${projectName} to Hetzner...`);

      const response = await fetch(`http://${device.ipAddress}:18080/projects/deploy`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          project_name: projectName,
          environment: 'development',
        }),
      });

      const task = await response.json();
      Alert.alert('Deployment Started', `Task ID: ${task.id}`);
    } catch (error) {
      Alert.alert('Deployment Failed', error instanceof Error ? error.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  const enableHotReload = async (projectName: string) => {
    try {
      setLoading(true);
      setLoadingMessage(`Enabling hot-reload for ${projectName}...`);

      const response = await fetch(`http://${device.ipAddress}:18080/projects/hot-reload`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project_name: projectName }),
      });

      const task = await response.json();
      Alert.alert('Hot-Reload Enabled', `Task ID: ${task.id}`);
    } catch (error) {
      Alert.alert('Hot-Reload Failed', error instanceof Error ? error.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  const setupMobileTesting = async () => {
    try {
      setLoading(true);
      setLoadingMessage('Setting up mobile testing...');

      const response = await fetch(`http://${device.ipAddress}:18080/mobile-test/setup`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          projects: ['yaver', 'talos', 'ocpp'],
        }),
      });

      const task = await response.json();
      Alert.alert('Mobile Testing Ready', 'Access all projects from your mobile device');
    } catch (error) {
      Alert.alert('Setup Failed', error instanceof Error ? error.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  const executeWorkflow = async () => {
    try {
      setLoading(true);
      setLoadingMessage('Executing development workflow...');

      const response = await fetch(`http://${device.ipAddress}:18080/workflow/execute`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          projects: ['yaver', 'talos', 'ocpp'],
          test_mode: selectedWorkflow === 'mobile' ? 'mobile' : 'web',
          agent_id: selectedAgent,
        }),
      });

      const data = await response.json();
      Alert.alert('Workflow Started', `${data.tasks.length} tasks created`);
    } catch (error) {
      Alert.alert('Workflow Failed', error instanceof Error ? error.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  const switchAgent = async (agentId: string) => {
    try {
      const response = await fetch(`http://${device.ipAddress}:18080/agent/switch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_id: agentId }),
      });

      const data = await response.json();
      if (data.success) {
        setSelectedAgent(agentId);
        Alert.alert('Agent Switched', data.message);
      }
    } catch (error) {
      Alert.alert('Switch Failed', error instanceof Error ? error.message : 'Unknown error');
    }
  };

  const getStatusStyle = (status: string) => {
    switch (status.toLowerCase()) {
      case 'idle':
        return styles.statusIdle;
      case 'building':
        return styles.statusBuilding;
      case 'deployed':
        return styles.statusDeployed;
      case 'testing':
        return styles.statusTesting;
      case 'failed':
        return styles.statusFailed;
      default:
        return styles.statusIdle;
    }
  };

  const getStatusText = (status: string) => {
    return status.toUpperCase();
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <Text style={styles.headerTitle}>Development Studio</Text>
        <Text style={styles.headerSubtitle}>Manage Yaver, Talos, OCPP on Hetzner</Text>
      </View>

      <ScrollView style={styles.projectsList}>
        {/* Agent Selection */}
        <View style={styles.agentSelector}>
          <Text style={styles.agentSelectorTitle}>Active AI Agent</Text>
          <View style={styles.agentButtons}>
            {['opencode', 'codex', 'claude'].map((agent) => (
              <TouchableOpacity
                key={agent}
                style={[
                  styles.agentButton,
                  selectedAgent === agent && styles.agentButtonSelected,
                ]}
                onPress={() => switchAgent(agent)}
              >
                <Text style={styles.agentButtonText}>
                  {agent.charAt(0).toUpperCase() + agent.slice(1)}
                </Text>
              </TouchableOpacity>
            ))}
          </View>
        </View>

        {/* Projects List */}
        <Text style={styles.workflowTitle}>Projects</Text>
        {Object.entries(projects).map(([name, project]) => (
          <View key={name} style={styles.projectCard}>
            <View style={styles.projectHeader}>
              <Text style={styles.projectName}>{name.charAt(0).toUpperCase() + name.slice(1)}</Text>
              <View style={[styles.projectStatus, getStatusStyle(project.status)]}>
                <Text style={styles.statusButtonText}>{getStatusText(project.status)}</Text>
              </View>
            </View>

            <View style={styles.projectInfo}>
              <View style={styles.infoRow}>
                <Text style={styles.infoLabel}>Environment:</Text>
                <Text style={styles.infoValue}>{project.environment}</Text>
              </View>
              <View style={styles.infoRow}>
                <Text style={styles.infoLabel}>Port:</Text>
                <Text style={styles.infoValue}>{project.port}</Text>
              </View>
              <View style={styles.infoRow}>
                <Text style={styles.infoLabel}>Last Deploy:</Text>
                <Text style={styles.infoValue}>
                  {project.last_deploy ? new Date(project.last_deploy).toLocaleString() : 'Never'}
                </Text>
              </View>
            </View>

            <View style={styles.projectActions}>
              <TouchableOpacity
                style={styles.actionButtonPrimary}
                onPress={() => deployProject(name)}
              >
                <Text style={styles.actionButtonText}>Deploy</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={styles.actionButtonSecondary}
                onPress={() => enableHotReload(name)}
              >
                <Text style={styles.actionButtonText}>Hot-Reload</Text>
              </TouchableOpacity>
            </View>
          </View>
        ))}

        {/* Workflow Section */}
        <View style={styles.workflowSection}>
          <Text style={styles.workflowTitle}>Development Workflows</Text>
          <View style={styles.workflowOptions}>
            {[
              {
                id: 'full',
                title: 'Full Development Cycle',
                description: 'Deploy all projects + mobile testing + hot-reload',
              },
              {
                id: 'mobile',
                title: 'Mobile Testing Only',
                description: 'Setup mobile access for all projects',
              },
              {
                id: 'quick',
                title: 'Quick Deploy',
                description: 'Deploy projects without testing setup',
              },
            ].map((workflow) => (
              <TouchableOpacity
                key={workflow.id}
                style={[
                  styles.workflowOption,
                  selectedWorkflow === workflow.id && styles.workflowOptionSelected,
                ]}
                onPress={() => setSelectedWorkflow(workflow.id)}
              >
                <Text style={styles.workflowOptionTitle}>{workflow.title}</Text>
                <Text style={styles.workflowOptionDescription}>{workflow.description}</Text>
              </TouchableOpacity>
            ))}
          </View>

          <TouchableOpacity style={styles.workflowButton} onPress={executeWorkflow}>
            <Text style={styles.workflowButtonText}>Execute Workflow</Text>
          </TouchableOpacity>
        </View>

        {/* Active Tasks */}
        {activeTasks.length > 0 && (
          <View style={styles.tasksList}>
            <Text style={styles.workflowTitle}>Active Tasks</Text>
            {activeTasks.map((task) => (
              <View key={task.id} style={styles.taskCard}>
                <View style={styles.taskHeader}>
                  <Text style={styles.taskId}>{task.id}</Text>
                  <Text style={[styles.taskStatus, getStatusStyle(task.status)]}>
                    {getStatusText(task.status)}
                  </Text>
                </View>
                <Text style={styles.taskProject}>{task.project_name}</Text>
                {task.output && (
                  <Text style={styles.taskOutput} numberOfLines={3}>
                    {task.output}
                  </Text>
                )}
                {task.error && (
                  <Text style={[styles.taskOutput, { color: '#ff4444' }]} numberOfLines={3}>
                    Error: {task.error}
                  </Text>
                )}
              </View>
            ))}
          </View>
        )}
      </ScrollView>

      {loading && (
        <View style={styles.loadingOverlay}>
          <ActivityIndicator size="large" color="#6366f1" />
          <Text style={styles.loadingText}>{loadingMessage}</Text>
        </View>
      )}
    </SafeAreaView>
  );
};

export default DevelopmentStudio;