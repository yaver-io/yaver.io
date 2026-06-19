import React, { useState, useEffect, useRef } from 'react';
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  Alert,
  PermissionsAndroid,
  Platform,
  SafeAreaView,
} from 'react-native';
import { useRouter, useLocalSearchParams } from 'expo-router';
import RoomView from 'livekit-react-native';
import { useLiveKitClient } from 'livekit-react-native';
import { QuicClient } from '@/lib/quic';

interface MeetingRoom {
  id: string;
  slug: string;
  title: string;
  description?: string;
  provider: 'yaver-native' | 'zoom' | 'google-meet' | 'microsoft-teams';
  adapterMode: 'native-sfu' | 'official-media-api' | 'remote-browser' | 'link-only' | 'pstn-audio-bridge';
  joinUrl: string;
  liveKitUrl?: string;
  liveKitApiKey?: string;
  liveKitApiSecret?: string;
  liveKitRoomName?: string;
  allowGuests: boolean;
  requireLobby: boolean;
  media?: {
    status: string;
  };
  pstn?: {
    status: string;
    dialInNumber?: string;
    pin?: string;
  };
}

export default function MeetingRoomScreen() {
  const router = useRouter();
  const params = useLocalSearchParams();
  const { client, connect, disconnect } = useLiveKitClient();

  // Initialize QuicClient (it uses AsyncStorage internally)
  const [quicClient] = useState(() => new QuicClient());

  const [room, setRoom] = useState<MeetingRoom | null>(null);
  const [loading, setLoading] = useState(true);
  const [connected, setConnected] = useState(false);
  const [micEnabled, setMicEnabled] = useState(false);
  const [cameraEnabled, setCameraEnabled] = useState(false);
  const [participantCount, setParticipantCount] = useState(0);
  const [token, setToken] = useState<string | null>(null);

  const slug = params.slug as string;

  useEffect(() => {
    loadRoomDetails();
  }, [slug]);

  const loadRoomDetails = async () => {
    try {
      const baseUrl = quicClient.baseUrl;

      // Get auth headers
      const headers = quicClient.getAuthHeaders();

      // Load room details
      const response = await fetch(`${baseUrl}/meeting-rooms`, { headers });
      const data = await response.json();
      const foundRoom = data.rooms?.find((r: MeetingRoom) => r.slug === slug);

      if (foundRoom) {
        setRoom(foundRoom);
        // Request join token
        await requestJoinToken(foundRoom, baseUrl, headers);
      } else {
        Alert.alert('Error', 'Room not found');
        router.back();
      }
    } catch (error) {
      Alert.alert('Error', 'Failed to load room details');
      router.back();
    } finally {
      setLoading(false);
    }
  };

  const requestJoinToken = async (roomData: MeetingRoom, baseUrl: string, headers: Record<string, string>) => {
    try {
      const response = await fetch(`${baseUrl}/call/${roomData.slug}/join`, {
        method: 'POST',
        headers: {
          ...headers,
          'Content-Type': 'application/json',
        },
      });

      if (response.ok) {
        const data = await response.json();
        setToken(data.token);
      }
    } catch (error) {
      console.error('Failed to request join token:', error);
      Alert.alert('Error', 'Failed to join room');
    }
  };

  const requestPermissions = async () => {
    if (Platform.OS === 'android') {
      const granted = await PermissionsAndroid.requestMultiple([
        PermissionsAndroid.PERMISSIONS.CAMERA,
        PermissionsAndroid.PERMISSIONS.RECORD_AUDIO,
      ]);

      return (
        granted['android.permission.CAMERA'] === PermissionsAndroid.RESULTS.GRANTED &&
        granted['android.permission.RECORD_AUDIO'] === PermissionsAndroid.RESULTS.GRANTED
      );
    }

    return true; // iOS handles permissions at runtime
  };

  const handleJoin = async () => {
    if (!room || !token) return;

    const hasPermissions = await requestPermissions();
    if (!hasPermissions) {
      Alert.alert('Error', 'Camera and microphone permissions are required');
      return;
    }

    try {
      // Connect to LiveKit room
      await connect(room.liveKitUrl || '', token);
      setConnected(true);
      setMicEnabled(true);
      setCameraEnabled(true);
    } catch (error) {
      console.error('Failed to connect:', error);
      Alert.alert('Error', 'Failed to connect to room');
    }
  };

  const handleLeave = async () => {
    try {
      await disconnect();
      setConnected(false);
      setMicEnabled(false);
      setCameraEnabled(false);
      router.back();
    } catch (error) {
      console.error('Failed to disconnect:', error);
    }
  };

  const toggleMic = async () => {
    if (!client) return;
    try {
      const newMicEnabled = !micEnabled;
      // Toggle microphone
      setMicEnabled(newMicEnabled);
    } catch (error) {
      console.error('Failed to toggle microphone:', error);
    }
  };

  const toggleCamera = async () => {
    if (!client) return;
    try {
      const newCameraEnabled = !cameraEnabled;
      // Toggle camera
      setCameraEnabled(newCameraEnabled);
    } catch (error) {
      console.error('Failed to toggle camera:', error);
    }
  };

  if (loading) {
    return (
      <SafeAreaView style={[styles.container, styles.loading]}>
        <Text style={styles.loadingText}>Loading room...</Text>
      </SafeAreaView>
    );
  }

  if (!room) {
    return (
      <SafeAreaView style={[styles.container, styles.error]}>
        <Text style={styles.errorText}>Room not found</Text>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={styles.container}>
      {/* Header */}
      <View style={styles.header}>
        <TouchableOpacity style={styles.backButton} onPress={router.back}>
          <Text style={styles.backButtonText}>←</Text>
        </TouchableOpacity>
        <View style={styles.headerContent}>
          <Text style={styles.title} numberOfLines={1}>
            {room.title}
          </Text>
          {connected && (
            <Text style={styles.participantCount}>
              {participantCount} participant{participantCount !== 1 ? 's' : ''}
            </Text>
          )}
        </View>
      </View>

      {/* Content */}
      <ScrollView style={styles.content} contentContainerStyle={styles.contentContainer}>
        {/* Video Grid */}
        {connected ? (
          <View style={styles.videoGrid}>
            <RoomView style={styles.roomView} />
          </View>
        ) : (
          <View style={styles.preview}>
            <Text style={styles.previewText}>
              {room.description || 'No description provided'}
            </Text>
            <View style={styles.previewInfo}>
              <Text style={styles.previewLabel}>Provider:</Text>
              <Text style={styles.previewValue}>{room.provider}</Text>
            </View>
            <View style={styles.previewInfo}>
              <Text style={styles.previewLabel}>Mode:</Text>
              <Text style={styles.previewValue}>{room.adapterMode}</Text>
            </View>
            {room.pstn?.status === 'enabled' && (
              <>
                <View style={styles.previewInfo}>
                  <Text style={styles.previewLabel}>Dial-in:</Text>
                  <Text style={styles.previewValue}>{room.pstn.dialInNumber}</Text>
                </View>
                <View style={styles.previewInfo}>
                  <Text style={styles.previewLabel}>PIN:</Text>
                  <Text style={styles.previewValue}>{room.pstn.pin}</Text>
                </View>
              </>
            )}
          </View>
        )}

        {/* Room Details */}
        <View style={styles.details}>
          <View style={styles.detailRow}>
            <Text style={styles.detailLabel}>Join URL:</Text>
            <Text style={styles.detailValue} numberOfLines={2}>
              {room.joinUrl}
            </Text>
          </View>
          {room.allowGuests && (
            <View style={styles.detailRow}>
              <Text style={styles.detailLabel}>Guest Access:</Text>
              <Text style={styles.detailValue}>Allowed</Text>
            </View>
          )}
          {room.requireLobby && (
            <View style={styles.detailRow}>
              <Text style={styles.detailLabel}>Lobby:</Text>
              <Text style={styles.detailValue}>Required</Text>
            </View>
          )}
        </View>
      </ScrollView>

      {/* Controls */}
      <View style={styles.controls}>
        {connected ? (
          <>
            <TouchableOpacity
              style={[styles.controlButton, micEnabled ? styles.controlButtonActive : styles.controlButtonInactive]}
              onPress={toggleMic}
            >
              <Text style={styles.controlButtonText}>{micEnabled ? '🎤' : '🔇'}</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={[styles.controlButton, cameraEnabled ? styles.controlButtonActive : styles.controlButtonInactive]}
              onPress={toggleCamera}
            >
              <Text style={styles.controlButtonText}>{cameraEnabled ? '📷' : '🚫'}</Text>
            </TouchableOpacity>
            <TouchableOpacity style={[styles.controlButton, styles.controlButtonLeave]} onPress={handleLeave}>
              <Text style={styles.controlButtonText}>📞</Text>
            </TouchableOpacity>
          </>
        ) : (
          <TouchableOpacity style={[styles.controlButton, styles.controlButtonJoin]} onPress={handleJoin}>
            <Text style={styles.controlButtonText}>Join Call</Text>
          </TouchableOpacity>
        )}
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#000000',
  },
  loading: {
    justifyContent: 'center',
    alignItems: 'center',
  },
  loadingText: {
    color: '#ffffff',
    fontSize: 16,
  },
  error: {
    justifyContent: 'center',
    alignItems: 'center',
  },
  errorText: {
    color: '#ff4444',
    fontSize: 16,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    padding: 16,
    borderBottomWidth: 1,
    borderBottomColor: '#333333',
  },
  backButton: {
    marginRight: 12,
  },
  backButtonText: {
    color: '#ffffff',
    fontSize: 24,
    fontWeight: 'bold',
  },
  headerContent: {
    flex: 1,
  },
  title: {
    color: '#ffffff',
    fontSize: 18,
    fontWeight: '600',
  },
  participantCount: {
    color: '#888888',
    fontSize: 14,
    marginTop: 2,
  },
  content: {
    flex: 1,
  },
  contentContainer: {
    padding: 16,
  },
  videoGrid: {
    aspectRatio: 16 / 9,
    backgroundColor: '#111111',
    borderRadius: 12,
    overflow: 'hidden',
    marginBottom: 16,
  },
  roomView: {
    flex: 1,
  },
  preview: {
    backgroundColor: '#111111',
    borderRadius: 12,
    padding: 20,
    marginBottom: 16,
  },
  previewText: {
    color: '#ffffff',
    fontSize: 16,
    marginBottom: 16,
  },
  previewInfo: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginBottom: 8,
  },
  previewLabel: {
    color: '#888888',
    fontSize: 14,
  },
  previewValue: {
    color: '#ffffff',
    fontSize: 14,
    fontWeight: '500',
  },
  details: {
    backgroundColor: '#111111',
    borderRadius: 12,
    padding: 16,
  },
  detailRow: {
    marginBottom: 12,
  },
  detailLabel: {
    color: '#888888',
    fontSize: 14,
    marginBottom: 4,
  },
  detailValue: {
    color: '#ffffff',
    fontSize: 14,
    fontWeight: '500',
  },
  controls: {
    flexDirection: 'row',
    justifyContent: 'space-around',
    padding: 16,
    borderTopWidth: 1,
    borderTopColor: '#333333',
  },
  controlButton: {
    width: 56,
    height: 56,
    borderRadius: 28,
    justifyContent: 'center',
    alignItems: 'center',
  },
  controlButtonActive: {
    backgroundColor: '#22c55e',
  },
  controlButtonInactive: {
    backgroundColor: '#333333',
  },
  controlButtonLeave: {
    backgroundColor: '#ef4444',
  },
  controlButtonJoin: {
    backgroundColor: '#3b82f6',
    width: '100%',
  },
  controlButtonText: {
    color: '#ffffff',
    fontSize: 24,
  },
});