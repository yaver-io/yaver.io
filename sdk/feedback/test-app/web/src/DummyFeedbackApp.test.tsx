import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DummyFeedbackApp } from './DummyFeedbackApp';
import { YaverFeedback } from '../../../web/src';

const mockFetch = jest.fn();

Object.defineProperty(globalThis, 'fetch', {
  value: mockFetch,
  writable: true,
});

Object.defineProperty(navigator, 'platform', {
  value: 'MacIntel',
  configurable: true,
});

Object.defineProperty(navigator, 'userAgent', {
  value: 'Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36',
  configurable: true,
});

Object.defineProperty(navigator, 'appVersion', {
  value: 'Chrome/124.0.0.0',
  configurable: true,
});

describe('DummyFeedbackApp', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({
        id: 'report-qwen-123',
        changeSet: {
          id: 'changeset-1',
          feedbackId: 'report-qwen-123',
          status: 'candidate_ready',
          candidateLabel: 'ollama-qwen-vibe',
          createdAt: '2026-04-22T10:00:00.000Z',
          updatedAt: '2026-04-22T10:01:00.000Z',
        },
      }),
    });
  });

  afterEach(() => {
    const button = document.getElementById('yaver-feedback-btn');
    const overlay = document.getElementById('yaver-feedback-overlay');
    button?.remove();
    overlay?.remove();
  });

  it('runs a React host-app feedback scenario for an Ollama Qwen vibe change', async () => {
    render(<DummyFeedbackApp />);

    expect(screen.getByTestId('tone-copy')).toHaveTextContent(
      'Steady shipping UI for baseline review.'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Apply Ollama Qwen vibe' }));
    expect(screen.getByTestId('tone-copy')).toHaveTextContent(
      'Vibing shipping UI driven by Ollama Qwen copy.'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Send web feedback' }));

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        'http://127.0.0.1:18080/feedback',
        expect.objectContaining({ method: 'POST' })
      );
    });

    const request = mockFetch.mock.calls[0][1] as { body: FormData; headers: Record<string, string> };
    const metadata = JSON.parse(String(request.body.get('metadata')));

    expect(request.headers.Authorization).toBe('Bearer sdk-test-token');
    expect(metadata.transcript).toContain('Ollama Qwen');
    expect(metadata.transcript).toContain('steady to vibing');
    expect(metadata.project.projectName).toBe('feedback-web-dummy-react');
    expect(metadata.candidate.label).toBe('ollama-qwen-vibe');
    expect(request.body.get('screenshot_0')).toBeInstanceOf(Blob);

    await waitFor(() => {
      expect(screen.getByTestId('feedback-status')).toHaveTextContent(
        'Feedback sent: report-qwen-123'
      );
    });

    expect(screen.getByTestId('report-id')).toHaveTextContent('report-qwen-123');
    expect(YaverFeedback.getLastUploadResult()?.changeSet?.candidateLabel).toBe('ollama-qwen-vibe');
  });
});
