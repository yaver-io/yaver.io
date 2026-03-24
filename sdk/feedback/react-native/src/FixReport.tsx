import React, { useState } from 'react';
import {
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import type { TestFix, TestSession } from './types';

export interface FixReportProps {
  /** Test session data with fixes list */
  session: TestSession | null;
  /** Whether the modal is visible */
  visible: boolean;
  /** Called when the user closes the report */
  onClose: () => void;
  /** Accent color (matches FloatingButton) */
  color?: string;
}

/**
 * Fix Report viewer — shows a markdown-style list of all fixes
 * applied by the AI agent during a test session.
 *
 * Each fix shows the file, description, and error that triggered it.
 * Tap a fix to expand and see the diff/code snippet.
 *
 * Fixes are NOT committed — they're staged changes the developer
 * can review, accept, or revert.
 */
export const FixReport: React.FC<FixReportProps> = ({
  session,
  visible,
  onClose,
  color = '#6366f1',
}) => {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const fixes = session?.fixes ?? [];

  return (
    <Modal visible={visible} animationType="slide" transparent>
      <View style={s.overlay}>
        <View style={s.container}>
          {/* Header */}
          <View style={s.header}>
            <View style={{ flex: 1 }}>
              <Text style={s.title}>Test Report</Text>
              {session && (
                <Text style={s.subtitle}>
                  {session.screensTested}/{session.screensDiscovered} screens
                  {' \u00B7 '}
                  {session.errorsFound} errors
                  {' \u00B7 '}
                  {fixes.length} fixes
                </Text>
              )}
            </View>
            <TouchableOpacity onPress={onClose} style={s.closeBtn}>
              <Text style={s.closeBtnText}>{'\u2715'}</Text>
            </TouchableOpacity>
          </View>

          {/* Status bar */}
          {session?.active && (
            <View style={[s.statusBar, { backgroundColor: `${color}20` }]}>
              <View style={[s.statusDot, { backgroundColor: color }]} />
              <Text style={[s.statusText, { color }]}>
                {session.status || 'Testing...'}
              </Text>
            </View>
          )}

          {/* Fix list */}
          <ScrollView style={s.list} contentContainerStyle={s.listContent}>
            {fixes.length === 0 ? (
              <View style={s.empty}>
                <Text style={s.emptyText}>
                  {session?.active
                    ? 'Agent is exploring the app...'
                    : 'No fixes yet. Start a test session.'}
                </Text>
              </View>
            ) : (
              fixes.map((fix, i) => (
                <FixItem
                  key={fix.id}
                  fix={fix}
                  index={i + 1}
                  expanded={expanded.has(fix.id)}
                  onToggle={() => toggleExpand(fix.id)}
                  color={color}
                />
              ))
            )}
          </ScrollView>

          {/* Summary footer */}
          {fixes.length > 0 && !session?.active && (
            <View style={s.footer}>
              <Text style={s.footerText}>
                {fixes.filter((f) => f.verified).length}/{fixes.length} verified
                {' \u00B7 '}
                Changes are staged, not committed.
              </Text>
            </View>
          )}
        </View>
      </View>
    </Modal>
  );
};

function FixItem({
  fix,
  index,
  expanded,
  onToggle,
  color,
}: {
  fix: TestFix;
  index: number;
  expanded: boolean;
  onToggle: () => void;
  color: string;
}) {
  return (
    <Pressable onPress={onToggle} style={s.fixItem}>
      {/* Fix header */}
      <View style={s.fixHeader}>
        <View style={[s.fixIndex, { backgroundColor: fix.verified ? '#22c55e20' : `${color}20` }]}>
          <Text style={[s.fixIndexText, { color: fix.verified ? '#22c55e' : color }]}>
            {fix.verified ? '\u2713' : index}
          </Text>
        </View>
        <View style={{ flex: 1 }}>
          <Text style={s.fixDesc}>{fix.description}</Text>
          <Text style={s.fixFile}>
            {fix.file}{fix.line ? `:${fix.line}` : ''}
          </Text>
        </View>
        <Text style={s.expandIcon}>{expanded ? '\u25B2' : '\u25BC'}</Text>
      </View>

      {/* Error that triggered fix */}
      {fix.error && (
        <View style={s.fixError}>
          <Text style={s.fixErrorText}>{fix.error}</Text>
        </View>
      )}

      {/* Expanded: diff/code */}
      {expanded && fix.diff && (
        <View style={s.diffBlock}>
          <Text style={s.diffText}>{fix.diff}</Text>
        </View>
      )}
    </Pressable>
  );
}

const s = StyleSheet.create({
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.8)',
    justifyContent: 'flex-end',
  },
  container: {
    backgroundColor: '#0a0a0a',
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    maxHeight: '85%',
    borderWidth: 1,
    borderColor: '#1a1a1a',
    borderBottomWidth: 0,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    padding: 16,
    borderBottomWidth: 1,
    borderBottomColor: '#1a1a1a',
  },
  title: {
    fontSize: 16,
    fontWeight: '700',
    color: '#e5e5e5',
    fontFamily: 'Courier',
  },
  subtitle: {
    fontSize: 11,
    color: '#666',
    marginTop: 2,
    fontFamily: 'Courier',
  },
  closeBtn: { padding: 8 },
  closeBtnText: { color: '#666', fontSize: 16 },
  statusBar: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingVertical: 8,
    gap: 8,
  },
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
  },
  statusText: {
    fontSize: 11,
    fontWeight: '600',
    fontFamily: 'Courier',
  },
  list: { flex: 1 },
  listContent: { padding: 12, gap: 8 },
  empty: {
    padding: 32,
    alignItems: 'center',
  },
  emptyText: {
    color: '#444',
    fontSize: 13,
    fontFamily: 'Courier',
  },
  fixItem: {
    backgroundColor: '#111',
    borderRadius: 10,
    padding: 12,
    borderWidth: 1,
    borderColor: '#1a1a1a',
  },
  fixHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  fixIndex: {
    width: 24,
    height: 24,
    borderRadius: 12,
    alignItems: 'center',
    justifyContent: 'center',
  },
  fixIndexText: {
    fontSize: 11,
    fontWeight: '700',
    fontFamily: 'Courier',
  },
  fixDesc: {
    fontSize: 12,
    color: '#e5e5e5',
    fontWeight: '600',
  },
  fixFile: {
    fontSize: 10,
    color: '#666',
    fontFamily: 'Courier',
    marginTop: 2,
  },
  expandIcon: {
    color: '#444',
    fontSize: 10,
  },
  fixError: {
    marginTop: 8,
    backgroundColor: '#f8717110',
    borderRadius: 6,
    padding: 8,
  },
  fixErrorText: {
    fontSize: 10,
    color: '#f87171',
    fontFamily: 'Courier',
  },
  diffBlock: {
    marginTop: 8,
    backgroundColor: '#0d0d0d',
    borderRadius: 6,
    padding: 10,
    borderWidth: 1,
    borderColor: '#1a1a1a',
  },
  diffText: {
    fontSize: 10,
    color: '#22c55e',
    fontFamily: 'Courier',
    lineHeight: 16,
  },
  footer: {
    borderTopWidth: 1,
    borderTopColor: '#1a1a1a',
    padding: 12,
    alignItems: 'center',
  },
  footerText: {
    fontSize: 10,
    color: '#666',
    fontFamily: 'Courier',
  },
});
