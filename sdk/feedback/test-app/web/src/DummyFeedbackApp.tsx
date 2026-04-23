import React, { useEffect, useState } from 'react';
import { YaverFeedback, type FeedbackConfig, type DeviceInfo } from '../../../web/src';

const defaultConfig: FeedbackConfig = {
  enabled: true,
  trigger: 'manual',
  agentUrl: 'http://127.0.0.1:18080',
  authToken: 'sdk-test-token',
  autoLogin: false,
  appName: 'Dummy Web Feedback Harness',
  projectName: 'feedback-web-dummy-react',
  projectPath: '/tmp/feedback-web-dummy-react',
  surface: 'web',
  releaseChannel: 'candidate',
  candidate: {
    enabled: true,
    label: 'ollama-qwen-vibe',
    baseBranch: 'main',
    targetBranch: 'candidate/ollama-qwen-vibe',
    previewUrl: 'http://localhost:4173/preview',
  },
};

function readDeviceInfo(): DeviceInfo {
  return {
    platform: 'web',
    browser: navigator.userAgent.includes('Chrome') ? 'Chrome' : 'Unknown',
    browserVersion: navigator.appVersion,
    os: navigator.platform,
    screenSize: `${window.innerWidth}x${window.innerHeight}`,
    userAgent: navigator.userAgent,
  };
}

export function DummyFeedbackApp({
  feedbackConfig = defaultConfig,
}: {
  feedbackConfig?: FeedbackConfig;
}) {
  const [tone, setTone] = useState<'steady' | 'vibing'>('steady');
  const [status, setStatus] = useState('Waiting for feedback run.');
  const [reportId, setReportId] = useState<string | null>(null);

  useEffect(() => {
    void YaverFeedback.init(feedbackConfig);
  }, [feedbackConfig]);

  async function handleFeedback() {
    const transcript =
      tone === 'vibing'
        ? 'Shift the web UI character from steady to vibing through Ollama Qwen and keep the brighter copy.'
        : 'Keep the web UI steady for now.';

    YaverFeedback.captureScreenshot(`Character switched to ${tone}`);
    YaverFeedback.addAnnotation(`Web UI tone is ${tone}`);

    const id = await YaverFeedback.upload({
      metadata: {
        source: 'in-app-sdk',
        deviceInfo: readDeviceInfo(),
        url: window.location.href,
        timeline: [
          { time: 0, type: 'annotation', text: `Tone requested: ${tone}` },
          { time: 1, type: 'voice', text: transcript },
        ],
        transcript,
        project: {
          appName: feedbackConfig.appName,
          projectName: feedbackConfig.projectName,
          projectPath: feedbackConfig.projectPath,
          surface: feedbackConfig.surface,
          releaseChannel: feedbackConfig.releaseChannel,
        },
        candidate: feedbackConfig.candidate,
      },
      screenshots: [new Blob([`tone=${tone}`], { type: 'text/plain' })],
    });

    setReportId(id);
    setStatus(id ? `Feedback sent: ${id}` : 'Feedback failed');
  }

  return (
    <main>
      <h1>Feedback SDK Dummy React App</h1>
      <p data-testid="tone-copy">
        {tone === 'steady'
          ? 'Steady shipping UI for baseline review.'
          : 'Vibing shipping UI driven by Ollama Qwen copy.'}
      </p>
      <button type="button" onClick={() => setTone('vibing')}>
        Apply Ollama Qwen vibe
      </button>
      <button type="button" onClick={handleFeedback}>
        Send web feedback
      </button>
      <p data-testid="feedback-status">{status}</p>
      {reportId ? <p data-testid="report-id">{reportId}</p> : null}
    </main>
  );
}
