import React, { useCallback, useEffect, useRef, useState } from 'react';
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
  const activeOverlayRef = useRef<'none' | 'login' | 'guest' | 'picker'>('none');

  const openLogin = useCallback(() => {
    activeOverlayRef.current = 'login';
    setGuestVisible(false);
    setPickerVisible(false);
    setLoginVisible(true);
  }, []);

  const openGuest = useCallback(() => {
    activeOverlayRef.current = 'guest';
    setLoginVisible(false);
    setPickerVisible(false);
    setGuestVisible(true);
  }, []);

  const openPicker = useCallback(() => {
    activeOverlayRef.current = 'picker';
    setLoginVisible(false);
    setGuestVisible(false);
    setPickerVisible(true);
  }, []);

  const closeAll = useCallback(() => {
    activeOverlayRef.current = 'none';
    setLoginVisible(false);
    setGuestVisible(false);
    setPickerVisible(false);
  }, []);

  useEffect(() => {
    let mounted = true;
    (async () => {
      const cached = await getToken();
      if (mounted && cached) setToken(cached);
    })();

    const loginSub = DeviceEventEmitter.addListener(
      'yaverFeedback:startLogin',
      () => {
        if (activeOverlayRef.current !== 'none') return;
        openLogin();
      },
    );
    const pickerSub = DeviceEventEmitter.addListener(
      'yaverFeedback:startMachinePicker',
      async () => {
        if (activeOverlayRef.current !== 'none') return;
        const cached = await getToken();
        if (cached) setToken(cached);
        if (cached) openPicker();
      },
    );
    return () => {
      mounted = false;
      loginSub.remove();
      pickerSub.remove();
    };
  }, [openLogin, openPicker]);

  const continueAfterAuth = async (newToken: string, inviteCode?: string) => {
    setToken(newToken);
    await YaverFeedback.setAuthToken(newToken);
    const devices = await listReachableDevices(newToken).catch(() => ({ owned: [], shared: [] }));
    const cleanedInviteCode = (inviteCode ?? '').trim().toUpperCase();
    if (cleanedInviteCode) {
      setPendingInviteCode(cleanedInviteCode);
      openGuest();
      return;
    }
    if (devices.owned.length === 0 && devices.shared.length === 0) {
      openGuest();
      return;
    }
    openPicker();
  };

  const handleLoggedIn = async (newToken: string, opts?: { inviteCode?: string }) => {
    await continueAfterAuth(newToken, opts?.inviteCode);
  };

  const handleDevicePicked = async (device: RemoteDevice) => {
    await YaverFeedback.setPreferredDevice(device.deviceId);
    closeAll();
    // Continue straight into the feedback flow the user originally triggered.
    DeviceEventEmitter.emit('yaverFeedback:startReport');
  };

  return (
    <>
      <Modal
        visible={loginVisible}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={closeAll}
      >
        <YaverLoginScreen
          onLoggedIn={handleLoggedIn}
          onCancel={closeAll}
          initialInviteCode={pendingInviteCode ?? YaverFeedback.getConfig()?.guestInviteCode}
        />
      </Modal>

      <Modal
        visible={pickerVisible && !!token}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={closeAll}
      >
        {token && (
          <YaverMachinePickerScreen
            token={token}
            currentDeviceId={YaverFeedback.getConfig()?.preferredDeviceId}
            onPick={handleDevicePicked}
            onCancel={closeAll}
          />
        )}
      </Modal>

      <Modal
        visible={guestVisible && !!token}
        animationType="slide"
        presentationStyle="fullScreen"
        onRequestClose={closeAll}
      >
        {token && (
          <YaverGuestOnboardingScreen
            token={token}
            initialInviteCode={pendingInviteCode ?? YaverFeedback.getConfig()?.guestInviteCode}
            onContinue={() => {
              setPendingInviteCode(null);
              openPicker();
            }}
            onCancel={() => {
              setPendingInviteCode(null);
              openPicker();
            }}
          />
        )}
      </Modal>
    </>
  );
};
