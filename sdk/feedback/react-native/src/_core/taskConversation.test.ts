// AUTO-SYNCED from shared/client-core/src/taskConversation.test.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

import { strict as assert } from 'node:assert';
import {
  firstClassTaskConversationTurns,
  remoteAgentConversationView,
  remoteAgentStatusLabel,
} from './taskConversation';

const assistant = [{
  id: 'a', kind: 'message' as const, role: 'assistant' as const,
  text: 'I changed the task stream and the tests pass.', createdAt: '', updatedAt: '',
}];

assert.equal(remoteAgentConversationView({ status: 'running', presentation: assistant }).state, 'working');
const liveUpdate = remoteAgentConversationView({
  status: 'running',
  presentation: [
    { ...assistant[0], id: 'status', kind: 'status', role: undefined, text: 'Running verification.' },
    { ...assistant[0], text: 'I found the missing message update and am fixing the task view now.' },
  ],
}, { latestActivity: 'Run tests' });
assert.equal(liveUpdate.title, 'Latest update from the agent');
assert.equal(liveUpdate.detail, 'I found the missing message update and am fixing the task view now.');
assert.equal(liveUpdate.activity, 'Run tests');
const longReply = 'Readable result '.repeat(80);
const bounded = remoteAgentConversationView({
  status: 'completed',
  presentation: [{ ...assistant[0], text: longReply }],
});
assert.ok(bounded.detail.length <= 420, 'status-card summaries stay bounded');
assert.equal(bounded.assistantText, longReply.trim(), 'the complete semantic reply remains available to conversation consumers');
assert.equal(remoteAgentConversationView({ status: 'ready', presentation: assistant }).state, 'your_turn');
assert.equal(remoteAgentConversationView({ status: 'ready', presentation: assistant }).canCompose, true);
assert.equal(remoteAgentConversationView({ status: 'review', presentation: assistant }).title, 'The agent says the work is fully complete');
const rawOnlyFailure = remoteAgentConversationView({ status: 'failed' });
assert.equal(rawOnlyFailure.state, 'failed');
assert.equal(rawOnlyFailure.assistantText, undefined);
assert.doesNotMatch(rawOnlyFailure.detail, /raw-looking output/);
assert.equal(remoteAgentConversationView({ status: 'running' }, { pendingQuestion: 'Deploy now?' }).state, 'needs_answer');
assert.equal(remoteAgentStatusLabel('ready'), 'Your turn');

const projected = firstClassTaskConversationTurns([
  { role: 'user', content: 'Change the background.' },
  { role: 'assistant', content: '$ rg background\n@@ -1 +1 @@\nANSI runner dump' },
  { role: 'user', content: 'Make it warmer.' },
  { role: 'assistant', content: 'tool output that must stay folded' },
], [
  { ...assistant[0], id: 'a1', text: 'I changed the background and verified the screen.' },
  { ...assistant[0], id: 'a2', text: 'I warmed the color and the visual check passes.' },
]);
assert.deepEqual(projected.map(({ role, content }) => ({ role, content })), [
  { role: 'user', content: 'Change the background.' },
  { role: 'assistant', content: 'I changed the background and verified the screen.' },
  { role: 'user', content: 'Make it warmer.' },
  { role: 'assistant', content: 'I warmed the color and the visual check passes.' },
]);

assert.deepEqual(firstClassTaskConversationTurns([
  { role: 'user', content: 'Legacy request' },
  { role: 'assistant', content: '$ npm test\nsecret-adjacent path\ndiff --git' },
], []), [{ role: 'user', content: 'Legacy request' }]);

// A terminal transcript may be delivered as a diagnostic presentation message
// after the readable answer (for example an OpenCode tmux fallback). The
// primary phone conversation must still select the readable message. This is
// intentionally a client-side guard as well as Go-side sanitization: an old or
// third-party agent must not make a mobile update necessary to stay readable.
const readableAnswer = { ...assistant[0], id: 'human', text: 'Updated the SFMG background and checked the diff.' };
const terminalDetails = {
  ...assistant[0], id: 'raw-terminal', visibility: 'details' as const,
  text: "> build · model\n→ Edit app.json\nIndex: /workspace/sfmg/app.json\n__YAVER_EXIT__:0\nroot@host:/workspace#",
};
const readableView = remoteAgentConversationView({
  status: 'ready', presentation: [readableAnswer, terminalDetails],
});
assert.equal(readableView.assistantText, readableAnswer.text);
assert.deepEqual(firstClassTaskConversationTurns([
  { role: 'user', content: 'Change the background.' },
  { role: 'assistant', content: 'legacy persisted terminal transcript' },
], [readableAnswer, terminalDetails]), [
  { role: 'user', content: 'Change the background.' },
  { role: 'assistant', content: readableAnswer.text },
]);
