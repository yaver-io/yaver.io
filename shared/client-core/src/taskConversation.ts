import type { TaskPresentationMessage } from './taskPresentation';

export type RemoteAgentTaskStatus =
  | 'queued'
  | 'running'
  | 'ready'
  | 'review'
  | 'completed'
  | 'failed'
  | 'stopped'
  | string;

export type RemoteAgentConversationState =
  | 'queued'
  | 'working'
  | 'your_turn'
  | 'needs_answer'
  | 'review'
  | 'completed'
  | 'failed'
  | 'stopped';

export type RemoteAgentConversationTone = 'active' | 'attention' | 'success' | 'error' | 'muted';

export interface RemoteAgentConversationTask {
  status: RemoteAgentTaskStatus;
  presentation?: TaskPresentationMessage[];
  failure?: { title?: string; reason?: string; remedy?: string } | null;
}

export interface RemoteAgentConversationTurn {
  role: string;
  content: string;
  hidden?: boolean;
}

export interface RemoteAgentConversationView {
  state: RemoteAgentConversationState;
  tone: RemoteAgentConversationTone;
  eyebrow: string;
  title: string;
  detail: string;
  activity?: string;
  assistantText?: string;
  nextAction?: string;
  isCoding: boolean;
  canCompose: boolean;
  closesTurnStream: boolean;
}

function clean(value: unknown, max = 420): string {
  const text = String(value ?? '').replace(/\s+/g, ' ').trim();
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
}

function latest(messages: TaskPresentationMessage[], predicate: (message: TaskPresentationMessage) => boolean) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (predicate(messages[index])) return messages[index];
  }
  return undefined;
}

/**
 * The cross-surface remote-agent conversation contract.
 *
 * Presentation is the only runner-authored input. Raw stdout is deliberately
 * absent from the type: a terminal transcript is evidence, never conversation.
 */
export function remoteAgentConversationView(
  task: RemoteAgentConversationTask,
  options: { pendingQuestion?: string; latestActivity?: string } = {},
): RemoteAgentConversationView {
  const presentation = task.presentation ?? [];
  const assistant = latest(presentation, (message) =>
    message.visibility !== 'details' &&
    message.kind === 'message' && message.role === 'assistant' && !!message.text.trim());
  const stateMessage = latest(presentation, (message) =>
    message.kind !== 'message' && message.kind !== 'tool' && message.kind !== 'patch' && !!message.text.trim());
  const assistantText = clean(assistant?.text || '', 32 * 1024) || undefined;
  // `assistantText` remains the complete semantic reply for transcript and
  // voice consumers. Status cards need a bounded summary: putting a multi-KB
  // final answer into a pinned mobile status surface simply recreates the raw
  // transcript wall this contract replaced.
  const assistantSummary = assistantText ? clean(assistantText, 420) : '';
  const semanticActivity = clean(stateMessage?.text || options.latestActivity || '');
  const pendingQuestion = clean(options.pendingQuestion || '');

  if (pendingQuestion || stateMessage?.kind === 'action_required') {
    const question = pendingQuestion || clean(stateMessage?.text);
    return {
      state: 'needs_answer', tone: 'attention', eyebrow: 'NEEDS YOUR ANSWER',
      title: 'The agent is waiting for you',
      detail: question || 'Answer the open question to continue this turn.',
      activity: semanticActivity || undefined,
      assistantText,
      nextAction: 'Reply to the question below.',
      isCoding: false, canCompose: true, closesTurnStream: false,
    };
  }

  if (task.failure || task.status === 'failed') {
    return {
      state: 'failed', tone: 'error', eyebrow: 'NEEDS ATTENTION',
      title: clean(task.failure?.title) || 'The turn failed',
      detail: clean(task.failure?.reason) || assistantText || 'The runner stopped without a readable failure reason.',
      activity: semanticActivity || undefined,
      assistantText,
      nextAction: clean(task.failure?.remedy) || undefined,
      isCoding: false, canCompose: true, closesTurnStream: true,
    };
  }

  if (task.status === 'stopped') {
    return {
      state: 'stopped', tone: 'muted', eyebrow: 'STOPPED', title: 'The session was stopped',
      detail: assistantSummary || 'No runner work is active.', activity: semanticActivity || undefined,
      assistantText, isCoding: false, canCompose: true, closesTurnStream: true,
    };
  }

  if (task.status === 'completed') {
    return {
      state: 'completed', tone: 'success', eyebrow: 'COMPLETED', title: 'Work completed',
      detail: assistantSummary || 'The task is complete.', activity: semanticActivity || undefined,
      assistantText, isCoding: false, canCompose: false, closesTurnStream: true,
    };
  }

  if (task.status === 'review') {
    return {
      state: 'review', tone: 'success', eyebrow: 'READY TO REVIEW', title: 'The agent says the work is fully complete',
      detail: assistantSummary || semanticActivity || 'Review the result, then mark it complete or keep vibing.',
      activity: semanticActivity || undefined,
      assistantText, nextAction: 'Review the result or send another message.',
      isCoding: false, canCompose: true, closesTurnStream: true,
    };
  }

  if (task.status === 'ready') {
    return {
      state: 'your_turn', tone: 'muted', eyebrow: 'YOUR TURN', title: 'The agent replied',
      detail: assistantText
        ? 'Continue in the same runner conversation.'
        : 'This runner did not provide a clean assistant message. Its terminal output is available under Details.',
      activity: semanticActivity || undefined,
      assistantText, nextAction: 'Send a follow-up whenever you are ready.',
      isCoding: false, canCompose: true, closesTurnStream: true,
    };
  }

  if (task.status === 'queued') {
    return {
      state: 'queued', tone: 'active', eyebrow: 'QUEUED', title: 'Waiting to start',
      detail: semanticActivity || 'The runner has the message and will start shortly.',
      activity: semanticActivity || undefined,
      assistantText, isCoding: true, canCompose: false, closesTurnStream: false,
    };
  }

  // ACP-capable runners publish human narration while they work. That message
  // is the thing the person is waiting to read; tool activity is supporting
  // context. The old ordering ignored `assistantText` here, so a real agent
  // update was buried in the transcript while the prominent card showed a
  // shell-derived action instead.
  if (assistantSummary) {
    return {
      state: 'working', tone: 'active', eyebrow: 'WORKING', title: 'Latest update from the agent',
      detail: assistantSummary,
      activity: clean(options.latestActivity || semanticActivity) || undefined,
      assistantText, isCoding: true, canCompose: false, closesTurnStream: false,
    };
  }

  return {
    state: 'working', tone: 'active', eyebrow: 'WORKING', title: semanticActivity || 'The agent is working',
    detail: options.latestActivity ? clean(options.latestActivity) : 'The agent is working. A readable update will appear here as soon as it responds.',
    activity: semanticActivity || undefined,
    assistantText, isCoding: true, canCompose: false, closesTurnStream: false,
  };
}

/**
 * Project persisted task history into first-class chat turns.
 *
 * Persisted assistant turns are compatibility storage: older/raw runners may
 * have put a PTY transcript there. Presentation is the only runner-authored
 * conversation lane, so user turns retain their original positions while each
 * visible assistant slot is replaced by the corresponding semantic message.
 * Extra semantic messages are appended; raw-only assistant slots disappear
 * and remain available through the surface's Details/console disclosure.
 */
export function firstClassTaskConversationTurns(
  turns: RemoteAgentConversationTurn[] | null | undefined,
  presentation: TaskPresentationMessage[] | null | undefined,
): RemoteAgentConversationTurn[] {
  const semantic = (presentation ?? []).filter((message) =>
    message.visibility !== 'details' &&
    message.kind === 'message' && message.role === 'assistant' && !!message.text.trim());
  let semanticIndex = 0;
  const projected: RemoteAgentConversationTurn[] = [];

  for (const turn of turns ?? []) {
    if (turn.hidden === true) continue;
    if (turn.role === 'user') {
      if (String(turn.content ?? '').trim()) projected.push({ ...turn });
      continue;
    }
    if (turn.role !== 'assistant') continue;
    const message = semantic[semanticIndex++];
    if (!message) continue;
    projected.push({ role: 'assistant', content: message.text });
  }

  for (; semanticIndex < semantic.length; semanticIndex += 1) {
    projected.push({ role: 'assistant', content: semantic[semanticIndex].text });
  }
  return projected;
}

export function remoteAgentStatusLabel(status: RemoteAgentTaskStatus): string {
  switch (status) {
    case 'queued': return 'Queued';
    case 'running': return 'Working';
    case 'ready': return 'Your turn';
    case 'review': return 'Ready to review';
    case 'completed': return 'Completed';
    case 'failed': return 'Needs attention';
    case 'stopped': return 'Stopped';
    default: return clean(status) || 'Unknown';
  }
}
