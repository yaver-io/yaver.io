import React, { useEffect, useState } from 'react';
import { DeviceEventEmitter, Modal } from 'react-native';
import { YaverLoginScreen } from './LoginScreen';
import { YaverMachinePickerScreen } from './MachinePickerScreen';
import { YaverGuestOnboardingScreen } from './GuestOnboardingScreen';
import { YaverFeedback } from './YaverFeedback';
import { getToken, RemoteDevice, listReachableDevices } from './auth';

/**
 * Presentation layer for the SDK's auth + machine-picker modals.
 *
 * Mounts automatically inside `<FeedbackModal />`, so consumers of the SDK
 * get in-app login with no extra wiring. It listens for events emitted by
 * `YaverFeedback.showLogin()` / `showMachinePicker()`:
 *
 *   yaverFeedback:startLogin          → show login modal
 *   yaverFeedback:startMachinePicker  → show machine picker
 *
 * The overlay closes itself once login/pick succeeds, then re-emits
 * `yaverFeedback:startReport` so the user continues straight into the
 * feedback flow they originally triggered.
 */
export const AuthOverlay: React.FC = () => {
  const [loginVisible, setLoginVisible] = useState(false);
  const [guestVisible, setGuestVisible] = useState(false);
  const [pickerVisible, setPickerVisible] = useState(false);
  const [token, setToken] = useState<string | null>(null);
  const [pendingInviteCode, setPendingInviteCode] = useState<string | null>(null);

  useEffect(() => {
    let mounted = true;
    (async () => {
      const cached = await getToken();
      if (mounted && cached) setToken(cached);
    })();

    const loginSub = DeviceEventEmitter.addListener(
      'yaverFeedback:startLogin',
      () => setLoginVisible(true),
    );
    const pickerSub = DeviceEventEmitter.addListener(
      'yaverFeedback:startMachinePicker',
      async () => {
        const cached = await getToken();
        if (cached) setToken(cached);
        if (cached) setPickerVisible(true);
      },
    );
    return () => {
      mounted = false;
      loginSub.remove();
      pickerSub.remove();
    };
  }, []);

  const continueAfterAuth = async (newToken: string, inviteCode?: string) => {
    setToken(newToken);
    await YaverFeedback.setAuthToken(newToken);
    const devices = await listReachableDevices(newToken).catch(() => ({ owned: [], shared: [] }));
    setLoginVisible(false);
    const cleanedInviteCode = (inviteCode ?? '').trim().toUpperCase();
    if (cleanedInviteCode) {
      setPendingInviteCode(cleanedInviteCode);
      setGuestVisible(true);
      return;
    }
    if (devices.owned.length === 0 && devices.shared.length === 0) {
      setGuestVisible(true);
      return;
    }
    setPickerVisible(true);
  };

  const handleLoggedIn = async (newToken: string, opts?: { inviteCode?: string }) => {
    await continueAfterAuth(newToken, opts?.inviteCode);
  };

  const handleDevicePicked = async (device: RemoteDevice) => {
    await YaverFeedback.setPreferredDevice(device.deviceId);
    setPickerVisible(false);
    setGuestVisible(false);
    // Continue straight into the feedback flow the user originally triggered.
    DeviceEventEmitter.emit('yaverFeedback:startReport');
  };

  return (
    <>
      <Modal
        visible={loginVisible}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={() => setLoginVisible(false)}
      >
        <YaverLoginScreen
          onLoggedIn={handleLoggedIn}
          onCancel={() => setLoginVisible(false)}
          initialInviteCode={pendingInviteCode ?? YaverFeedback.getConfig()?.guestInviteCode}
        />
      </Modal>

      <Modal
        visible={pickerVisible && !!token}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={() => setPickerVisible(false)}
      >
        {token && (
          <YaverMachinePickerScreen
            token={token}
            currentDeviceId={YaverFeedback.getConfig()?.preferredDeviceId}
            onPick={handleDevicePicked}
            onCancel={() => setPickerVisible(false)}
          />
        )}
      </Modal>

      <Modal
        visible={guestVisible && !!token}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={() => setGuestVisible(false)}
      >
        {token && (
          <YaverGuestOnboardingScreen
            token={token}
            initialInviteCode={pendingInviteCode ?? YaverFeedback.getConfig()?.guestInviteCode}
            onContinue={() => {
              setGuestVisible(false);
              setPendingInviteCode(null);
              setPickerVisible(true);
            }}
            onCancel={() => {
              setGuestVisible(false);
              setPendingInviteCode(null);
              setPickerVisible(true);
            }}
          />
        )}
      </Modal>
    </>
  );
};
