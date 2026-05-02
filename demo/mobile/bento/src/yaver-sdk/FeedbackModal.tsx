import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  DeviceEventEmitter,
  FlatList,
  Modal,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import { BlackBox } from './BlackBox';
import { captureScreenshot, startAudioRecording, stopAudioRecording } from './capture';
import { uploadFeedback } from './upload';
import { TimelineEvent, DeviceInfo, FeedbackBundle, AgentCommentary } from './types';

type FeedbackMode = 'live' | 'narrated' | 'batch';

const MODE_LABELS: Record<FeedbackMode, string> = {
  live: 'Live',
  narrated: 'Narrated',
  batch: 'Batch',
};

/**
 * Full-screen modal for composing and sending a feedback report.
 * Renders when triggered by shake, floating button, or manual call.
 *
 * Supports three feedback modes:
 * - Live: stream events to the agent as they happen
 * - Narrated: record everything, send on stop
 * - Batch: dump everything at end (default)
 */
export const FeedbackModal: React.FC = () => {
  const [visible, setVisible] = useState(false);
  const [timeline, setTimeline] = useState<TimelineEvent[]>([]);
  const [isRecordingAudio, setIsRecordingAudio] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [mode, setMode] = useState<FeedbackMode>('batch');
  const [commentary, setCommentary] = useState<AgentCommentary[]>([]);
  const [isVoiceCommand, setIsVoiceCommand] = useState(false);
  const [isReloading, setIsReloading] = useState(false);
  const mountedRef = useRef(true);
  const commentaryListRef = useRef<FlatList>(null);

  useEffect(() => {
    mountedRef.current = true;
    const sub = DeviceEventEmitter.addListener('yaverFeedback:startReport', () => {
      if (YaverFeedback.isEnabled()) {
        setVisible(true);
        setTimeline([]);
        setError(null);
        setSent(false);
        setCommentary([]);
        setMode(YaverFeedback.getFeedbackMode());
      }
    });

    // Listen for agent commentary events
    const commentarySub = DeviceEventEmitter.addListener(
      'yaverFeedback:commentary',
      (event: AgentCommentary) => {
        if (mountedRef.current) {
          setCommentary((prev) => [...prev, event]);
        }
      },
    );

    return () => {
      mountedRef.current = false;
      sub.remove();
      commentarySub.remove();
    };
  }, []);

  const handleScreenshot = useCallback(async () => {
    try {
      const path = await captureScreenshot();
      if (mountedRef.current) {
        const event: TimelineEvent = {
          type: 'screenshot',
          path,
          timestamp: new Date().toISOString(),
        };
        setTimeline((prev) => [...prev, event]);

        // In live mode, stream the event immediately
        if (mode === 'live') {
          const client = YaverFeedback.getP2PClient();
          if (client) {
            try {
              await client.streamFeedback(
                (async function* () {
                  yield {
                    type: 'screenshot',
                    timestamp: event.timestamp,
                    data: { path },
                  };
                })(),
              );
            } catch (err) {
              console.warn('[YaverFeedback] Live stream failed:', err);
            }
          }
        }
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(String(err));
      }
    }
  }, [mode]);

  const handleToggleAudio = useCallback(async () => {
    if (isRecordingAudio) {
      try {
        const result = await stopAudioRecording();
        if (mountedRef.current) {
          setIsRecordingAudio(false);
          const event: TimelineEvent = {
            type: 'audio',
            path: result.path,
            timestamp: new Date().toISOString(),
            duration: result.duration,
          };
          setTimeline((prev) => [...prev, event]);

          // In live mode, stream the audio event
          if (mode === 'live') {
            const client = YaverFeedback.getP2PClient();
            if (client) {
              try {
                await client.streamFeedback(
                  (async function* () {
                    yield {
                      type: 'audio',
                      timestamp: event.timestamp,
                      data: { path: result.path, duration: result.duration },
                    };
                  })(),
                );
              } catch (err) {
                console.warn('[YaverFeedback] Live stream failed:', err);
              }
            }
          }
        }
      } catch (err) {
        if (mountedRef.current) {
          setIsRecordingAudio(false);
          setError(String(err));
        }
      }
    } else {
      try {
        await startAudioRecording();
        if (mountedRef.current) {
          setIsRecordingAudio(true);
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(String(err));
        }
      }
    }
  }, [isRecordingAudio, mode]);

  const handleVoiceCommand = useCallback(async () => {
    if (isVoiceCommand) {
      // Stop voice command and send as a task
      try {
        const result = await stopAudioRecording();
        if (mountedRef.current) {
          setIsVoiceCommand(false);

          const client = YaverFeedback.getP2PClient();
          if (client) {
            try {
              await client.streamFeedback(
                (async function* () {
                  yield {
                    type: 'voice_command',
                    timestamp: new Date().toISOString(),
                    data: { path: result.path, duration: result.duration },
                  };
                })(),
              );
            } catch (err) {
              console.warn('[YaverFeedback] Voice command send failed:', err);
              if (mountedRef.current) {
                setError('Failed to send voice command.');
              }
            }
          }
        }
      } catch (err) {
        if (mountedRef.current) {
          setIsVoiceCommand(false);
          setError(String(err));
        }
      }
    } else {
      try {
        await startAudioRecording();
        if (mountedRef.current) {
          setIsVoiceCommand(true);
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(String(err));
        }
      }
    }
  }, [isVoiceCommand]);

  const handleReload = useCallback(async () => {
    const config = YaverFeedback.getConfig();
    if (!config?.agentUrl) return;

    setIsReloading(true);
    try {
      const response = await fetch(`${config.agentUrl.replace(/\/$/, '')}/exec`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${config.authToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ command: 'reload', type: 'hot-reload' }),
      });
      if (response.ok) {
        BlackBox.lifecycle('Hot reload triggered from feedback SDK');
      }
    } catch (err) {
      if (mountedRef.current) {
        setError('Reload failed: ' + String(err));
      }
    } finally {
      if (mountedRef.current) {
        setIsReloading(false);
      }
    }
  }, []);

  const handleSend = useCallback(async () => {
    const config = YaverFeedback.getConfig();
    if (!config || !config.agentUrl) return;

    setIsSending(true);
    setError(null);

    try {
      const { Dimensions } = require('react-native');
      const { width, height } = Dimensions.get('window');

      const deviceInfo: DeviceInfo = {
        platform: Platform.OS,
        osVersion: String(Platform.Version),
        model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
        screenWidth: width,
        screenHeight: height,
      };

      const screenshots = timeline
        .filter((e) => e.type === 'screenshot')
        .map((e) => e.path);

      const audioEvent = timeline.find((e) => e.type === 'audio');

      // Include captured errors from the error buffer
      const capturedErrors = YaverFeedback.getCapturedErrors();

      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          device: deviceInfo,
          app: {},
        },
        screenshots,
        audio: audioEvent?.path,
        errors: capturedErrors.length > 0 ? capturedErrors : undefined,
      };

      await uploadFeedback(config.agentUrl, config.authToken, bundle);

      if (mountedRef.current) {
        setSent(true);
        // Auto-close after a short delay
        setTimeout(() => {
          if (mountedRef.current) {
            setVisible(false);
          }
        }, 1500);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(String(err));
      }
    } finally {
      if (mountedRef.current) {
        setIsSending(false);
      }
    }
  }, [timeline]);

  const [queued, setQueued] = useState(false);

  const handleQueue = useCallback(async () => {
    const config = YaverFeedback.getConfig();
    if (!config || !config.agentUrl) return;

    setIsSending(true);
    setError(null);

    try {
      const { Dimensions } = require('react-native');
      const { width, height } = Dimensions.get('window');

      const screenshots = timeline
        .filter((e) => e.type === 'screenshot')
        .map((e) => e.path);
      const audioEvent = timeline.find((e) => e.type === 'audio');
      const capturedErrors = YaverFeedback.getCapturedErrors();

      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          device: {
            platform: Platform.OS,
            osVersion: String(Platform.Version),
            model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
            screenWidth: width,
            screenHeight: height,
          },
          app: {},
          userNote: 'Bug report from device testing',
        },
        screenshots,
        audio: audioEvent?.path,
        errors: capturedErrors.length > 0 ? capturedErrors : undefined,
      };

      const client = YaverFeedback.getP2PClient();
      if (client) {
        await client.addTodoItem(bundle);
      }

      if (mountedRef.current) {
        setQueued(true);
        setTimeout(() => {
          if (mountedRef.current) {
            setVisible(false);
          }
        }, 1200);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(String(err));
      }
    } finally {
      if (mountedRef.current) {
        setIsSending(false);
      }
    }
  }, [timeline]);

  const handleCancel = useCallback(() => {
    setVisible(false);
    setTimeline([]);
    setError(null);
    setSent(false);
    setQueued(false);
    setIsRecordingAudio(false);
    setIsVoiceCommand(false);
    setCommentary([]);
  }, []);

  const renderCommentaryItem = useCallback(
    ({ item }: { item: AgentCommentary }) => (
      <View style={styles.commentaryBubble}>
        <Text style={styles.commentaryType}>{item.type}</Text>
        <Text style={styles.commentaryMessage}>{item.message}</Text>
      </View>
    ),
    [],
  );

  if (!visible) return null;

  return (
    <Modal
      visible={visible}
      animationType="slide"
      transparent
      onRequestClose={handleCancel}
    >
      <View style={styles.overlay}>
        <View style={styles.modal}>
          <Text style={styles.title}>Send Feedback</Text>

          {/* Mode selector */}
          <View style={styles.modeSelector}>
            {(['live', 'narrated', 'batch'] as FeedbackMode[]).map((m) => (
              <TouchableOpacity
                key={m}
                style={[styles.modeButton, mode === m && styles.modeButtonActive]}
                onPress={() => setMode(m)}
              >
                <Text
                  style={[styles.modeButtonText, mode === m && styles.modeButtonTextActive]}
                >
                  {MODE_LABELS[m]}
                </Text>
              </TouchableOpacity>
            ))}
          </View>

          {/* Agent commentary (chat-like view) */}
          {commentary.length > 0 && (
            <FlatList
              ref={commentaryListRef}
              data={commentary}
              renderItem={renderCommentaryItem}
              keyExtractor={(item) => item.id}
              style={styles.commentaryList}
              onContentSizeChange={() =>
                commentaryListRef.current?.scrollToEnd({ animated: true })
              }
            />
          )}

          {/* Timeline of captured items */}
          {timeline.length > 0 && (
            <ScrollView style={styles.timeline} horizontal>
              {timeline.map((event, idx) => (
                <View key={idx} style={styles.timelineItem}>
                  <Text style={styles.timelineIcon}>
                    {event.type === 'screenshot'
                      ? '[img]'
                      : event.type === 'audio'
                        ? '[mic]'
                        : '[vid]'}
                  </Text>
                  <Text style={styles.timelineLabel}>{event.type}</Text>
                  {event.duration != null && (
                    <Text style={styles.timelineDuration}>
                      {Math.round(event.duration)}s
                    </Text>
                  )}
                </View>
              ))}
            </ScrollView>
          )}

          {/* Action buttons */}
          <View style={styles.actions}>
            <TouchableOpacity style={styles.actionButton} onPress={handleScreenshot}>
              <Text style={styles.actionText}>Take Screenshot</Text>
            </TouchableOpacity>

            <TouchableOpacity
              style={[styles.actionButton, isRecordingAudio && styles.actionButtonActive]}
              onPress={handleToggleAudio}
              disabled={isVoiceCommand}
            >
              <Text style={styles.actionText}>
                {isRecordingAudio ? 'Stop Recording' : 'Voice Note'}
              </Text>
            </TouchableOpacity>
          </View>

          {/* Hot Reload + Streaming status */}
          <View style={styles.actions}>
            <TouchableOpacity
              style={[styles.actionButton, styles.reloadButton]}
              onPress={handleReload}
              disabled={isReloading}
            >
              <Text style={styles.actionText}>
                {isReloading ? 'Reloading...' : 'Hot Reload'}
              </Text>
            </TouchableOpacity>

            <View style={[styles.actionButton, styles.streamingIndicator]}>
              <View style={[styles.streamingDot, BlackBox.isStreaming && styles.streamingDotActive]} />
              <Text style={styles.streamingText}>
                {BlackBox.isStreaming ? 'Streaming' : 'Not streaming'}
              </Text>
            </View>
          </View>

          {/* Voice command button */}
          {mode === 'live' && (
            <TouchableOpacity
              style={[styles.voiceCommandButton, isVoiceCommand && styles.voiceCommandActive]}
              onPress={handleVoiceCommand}
              disabled={isRecordingAudio}
            >
              <Text style={styles.voiceCommandText}>
                {isVoiceCommand ? 'Stop & Send Command' : 'Speak to Fix'}
              </Text>
            </TouchableOpacity>
          )}

          {/* Error display */}
          {error && <Text style={styles.error}>{error}</Text>}

          {/* Queue / Fix Now / Cancel */}
          <View style={styles.footer}>
            <TouchableOpacity style={styles.cancelButton} onPress={handleCancel}>
              <Text style={styles.cancelText}>Cancel</Text>
            </TouchableOpacity>

            {sent ? (
              <View style={styles.sendButton}>
                <Text style={styles.sendText}>Sent!</Text>
              </View>
            ) : queued ? (
              <View style={[styles.sendButton, { backgroundColor: '#f59e0b' }]}>
                <Text style={styles.sendText}>Queued!</Text>
              </View>
            ) : (
              <View style={styles.footerActions}>
                <TouchableOpacity
                  style={[styles.queueButton, isSending && styles.sendButtonDisabled]}
                  onPress={handleQueue}
                  disabled={isSending || timeline.length === 0}
                >
                  <Text style={styles.queueText}>Queue</Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={[styles.sendButton, isSending && styles.sendButtonDisabled]}
                  onPress={handleSend}
                  disabled={isSending || timeline.length === 0}
                >
                  {isSending ? (
                    <ActivityIndicator color="#fff" size="small" />
                  ) : (
                    <Text style={styles.sendText}>Fix Now</Text>
                  )}
                </TouchableOpacity>
              </View>
            )}
          </View>
        </View>
      </View>
    </Modal>
  );
};

const styles = StyleSheet.create({
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'flex-end',
  },
  modal: {
    backgroundColor: '#1a1a2e',
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    padding: 24,
    paddingBottom: 40,
    maxHeight: '90%',
  },
  title: {
    fontSize: 20,
    fontWeight: '700',
    color: '#fff',
    marginBottom: 12,
  },
  modeSelector: {
    flexDirection: 'row',
    gap: 8,
    marginBottom: 16,
  },
  modeButton: {
    flex: 1,
    paddingVertical: 8,
    borderRadius: 8,
    alignItems: 'center',
    backgroundColor: 'rgba(255,255,255,0.08)',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.1)',
  },
  modeButtonActive: {
    backgroundColor: 'rgba(99,102,241,0.3)',
    borderColor: '#6366f1',
  },
  modeButtonText: {
    color: '#999',
    fontSize: 13,
    fontWeight: '600',
  },
  modeButtonTextActive: {
    color: '#c7c8ff',
  },
  commentaryList: {
    maxHeight: 140,
    marginBottom: 12,
  },
  commentaryBubble: {
    backgroundColor: 'rgba(99,102,241,0.15)',
    borderRadius: 10,
    padding: 10,
    marginBottom: 6,
    borderLeftWidth: 3,
    borderLeftColor: '#6366f1',
  },
  commentaryType: {
    color: '#8b8bf5',
    fontSize: 10,
    fontWeight: '700',
    textTransform: 'uppercase',
    marginBottom: 2,
  },
  commentaryMessage: {
    color: '#d0d0e0',
    fontSize: 13,
    lineHeight: 18,
  },
  timeline: {
    maxHeight: 80,
    marginBottom: 16,
  },
  timelineItem: {
    alignItems: 'center',
    marginRight: 16,
    backgroundColor: 'rgba(255,255,255,0.1)',
    borderRadius: 12,
    padding: 10,
    minWidth: 70,
  },
  timelineIcon: {
    fontSize: 14,
    color: '#ccc',
    fontWeight: '600',
  },
  timelineLabel: {
    color: '#ccc',
    fontSize: 11,
    marginTop: 4,
  },
  timelineDuration: {
    color: '#999',
    fontSize: 10,
  },
  actions: {
    flexDirection: 'row',
    gap: 12,
    marginBottom: 12,
  },
  actionButton: {
    flex: 1,
    backgroundColor: 'rgba(99,102,241,0.2)',
    borderWidth: 1,
    borderColor: 'rgba(99,102,241,0.4)',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
  },
  actionButtonActive: {
    backgroundColor: 'rgba(239,68,68,0.3)',
    borderColor: 'rgba(239,68,68,0.6)',
  },
  actionText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '600',
  },
  voiceCommandButton: {
    backgroundColor: 'rgba(34,197,94,0.2)',
    borderWidth: 1,
    borderColor: 'rgba(34,197,94,0.4)',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    marginBottom: 12,
  },
  voiceCommandActive: {
    backgroundColor: 'rgba(34,197,94,0.4)',
    borderColor: '#22c55e',
  },
  voiceCommandText: {
    color: '#22c55e',
    fontSize: 14,
    fontWeight: '600',
  },
  error: {
    color: '#ef4444',
    fontSize: 12,
    marginBottom: 12,
  },
  footer: {
    flexDirection: 'row',
    gap: 12,
  },
  cancelButton: {
    flex: 1,
    paddingVertical: 14,
    alignItems: 'center',
    borderRadius: 12,
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.2)',
  },
  cancelText: {
    color: '#999',
    fontSize: 16,
    fontWeight: '600',
  },
  sendButton: {
    flex: 2,
    backgroundColor: '#6366f1',
    paddingVertical: 14,
    alignItems: 'center',
    borderRadius: 12,
  },
  sendButtonDisabled: {
    opacity: 0.5,
  },
  sendText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '700',
  },
  footerActions: {
    flex: 2,
    flexDirection: 'row',
    gap: 8,
  },
  queueButton: {
    flex: 1,
    backgroundColor: '#f59e0b',
    paddingVertical: 14,
    alignItems: 'center',
    borderRadius: 12,
  },
  queueText: {
    color: '#000',
    fontSize: 16,
    fontWeight: '700',
  },
  reloadButton: {
    backgroundColor: 'rgba(251,191,36,0.2)',
    borderColor: 'rgba(251,191,36,0.4)',
  },
  streamingIndicator: {
    flexDirection: 'row',
    justifyContent: 'center',
    backgroundColor: 'rgba(255,255,255,0.05)',
    borderColor: 'rgba(255,255,255,0.1)',
  },
  streamingDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: '#555',
    marginRight: 6,
  },
  streamingDotActive: {
    backgroundColor: '#22c55e',
  },
  streamingText: {
    color: '#999',
    fontSize: 12,
  },
});
