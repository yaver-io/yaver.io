// Phone code editor — Slice 2 of the phone-first dev stack
// (docs/phone-first-dev-stack.md). Lets a developer edit the
// project's `src/` tree on the phone itself: file tree on top, a
// multiline TextInput below for the open file, save / new / delete
// actions, dirty-state guard before navigating away.
//
// Storage flows through phoneSandboxSourceDefault.ts — production
// RN binding of the source store from Slice 1. The screen never
// touches expo-file-system directly so the path-safety + atomic-
// write guarantees from the source store hold for every edit.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../../src/context/ThemeContext";
import { AppBackButton } from "../../../src/components/AppBackButton";
import {
  deleteSourceFile,
  hasSource,
  listSourceFiles,
  readSourceFile,
  writeSourceFile,
} from "../../../src/lib/phoneSandboxSourceDefault";
import {
  isSourceFileNotFound,
  type SourceFileEntry,
  UnsafeSourcePathError,
} from "../../../src/lib/phoneSandboxSource";

const STARTER_FILE = "App.tsx";
const STARTER_CONTENT = `// ${STARTER_FILE} — your phone-authored app entry point.
// The phone-first dev loop runs this against the on-device SQLite
// sandbox so you don't need a desktop to iterate.

export default function App() {
  return null;
}
`;

export default function PhoneProjectCodeScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [files, setFiles] = useState<SourceFileEntry[]>([]);
  const [filesLoading, setFilesLoading] = useState(true);
  const [filesError, setFilesError] = useState<string | null>(null);

  const [openPath, setOpenPath] = useState<string | null>(null);
  /** Last-saved content for the open file, used to compute dirty state. */
  const [savedContent, setSavedContent] = useState<string>("");
  const [editorContent, setEditorContent] = useState<string>("");
  const [editorLoading, setEditorLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  const dirty = openPath !== null && editorContent !== savedContent;

  const refreshFiles = useCallback(async () => {
    if (!slugStr) return;
    setFilesLoading(true);
    setFilesError(null);
    try {
      const list = await listSourceFiles(slugStr);
      setFiles(list);
    } catch (e: any) {
      setFilesError(e?.message ?? String(e));
    } finally {
      setFilesLoading(false);
    }
  }, [slugStr]);

  useEffect(() => {
    void refreshFiles();
  }, [refreshFiles]);

  // First-run kindness: if the project has nothing in src/ yet,
  // drop a starter App.tsx so the user has a target the moment they
  // arrive. Doesn't fire on subsequent visits because hasSource
  // returns true once any file exists.
  useEffect(() => {
    if (!slugStr) return;
    let cancelled = false;
    (async () => {
      try {
        const exists = await hasSource(slugStr);
        if (cancelled || exists) return;
        await writeSourceFile(slugStr, STARTER_FILE, STARTER_CONTENT);
        await refreshFiles();
      } catch {
        // Non-fatal — user can hit "+" to create files manually.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [slugStr, refreshFiles]);

  const openFile = useCallback(
    async (relPath: string) => {
      if (dirty) {
        const ok = await new Promise<boolean>((resolve) =>
          Alert.alert(
            "Unsaved changes",
            `Discard changes to ${openPath}?`,
            [
              { text: "Cancel", style: "cancel", onPress: () => resolve(false) },
              { text: "Discard", style: "destructive", onPress: () => resolve(true) },
            ],
            { cancelable: true, onDismiss: () => resolve(false) },
          ),
        );
        if (!ok) return;
      }
      setEditorLoading(true);
      try {
        const content = await readSourceFile(slugStr, relPath);
        setOpenPath(relPath);
        setSavedContent(content);
        setEditorContent(content);
      } catch (e) {
        if (isSourceFileNotFound(e)) {
          Alert.alert("File missing", `${relPath} no longer exists. Refreshing the file list.`);
          void refreshFiles();
        } else {
          Alert.alert("Read failed", String((e as { message?: string })?.message ?? e));
        }
      } finally {
        setEditorLoading(false);
      }
    },
    [dirty, openPath, slugStr, refreshFiles],
  );

  const save = useCallback(async () => {
    if (!openPath) return;
    setSaving(true);
    try {
      await writeSourceFile(slugStr, openPath, editorContent);
      setSavedContent(editorContent);
      // Refresh the list so the size + mtime in the tree reflect the
      // new state without the user having to pull-to-refresh.
      void refreshFiles();
    } catch (e) {
      if (e instanceof UnsafeSourcePathError) {
        Alert.alert("Bad path", e.message);
      } else {
        Alert.alert("Save failed", String((e as { message?: string })?.message ?? e));
      }
    } finally {
      setSaving(false);
    }
  }, [openPath, slugStr, editorContent, refreshFiles]);

  const promptNewFile = useCallback(() => {
    if (Platform.OS === "ios") {
      Alert.prompt(
        "New file",
        "Relative path inside src/ (e.g. screens/Home.tsx)",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Create",
            onPress: async (input?: string) => {
              const relPath = (input ?? "").trim();
              if (!relPath) return;
              try {
                await writeSourceFile(slugStr, relPath, "");
                await refreshFiles();
                await openFile(relPath);
              } catch (e) {
                Alert.alert("Create failed", String((e as { message?: string })?.message ?? e));
              }
            },
          },
        ],
        "plain-text",
        "",
      );
      return;
    }
    // Android: Alert.prompt is iOS-only. Use a simple inline panel
    // instead — see NewFileInline below. Toggling state from here.
    setShowNewFileInline(true);
  }, [slugStr, refreshFiles, openFile]);

  const [showNewFileInline, setShowNewFileInline] = useState(false);
  const [newFilePathDraft, setNewFilePathDraft] = useState("");

  const submitNewFileInline = useCallback(async () => {
    const relPath = newFilePathDraft.trim();
    if (!relPath) return;
    try {
      await writeSourceFile(slugStr, relPath, "");
      setNewFilePathDraft("");
      setShowNewFileInline(false);
      await refreshFiles();
      await openFile(relPath);
    } catch (e) {
      Alert.alert("Create failed", String((e as { message?: string })?.message ?? e));
    }
  }, [newFilePathDraft, slugStr, refreshFiles, openFile]);

  const deleteFile = useCallback(
    (relPath: string) => {
      Alert.alert(
        `Delete ${relPath}?`,
        "This removes the file from your phone sandbox. The file isn't pushed anywhere yet, so the change is local until you push.",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Delete",
            style: "destructive",
            onPress: async () => {
              try {
                await deleteSourceFile(slugStr, relPath);
                if (openPath === relPath) {
                  setOpenPath(null);
                  setEditorContent("");
                  setSavedContent("");
                }
                await refreshFiles();
              } catch (e) {
                Alert.alert("Delete failed", String((e as { message?: string })?.message ?? e));
              }
            },
          },
        ],
      );
    },
    [openPath, slugStr, refreshFiles],
  );

  // Indent each row by its depth in the tree so a flat FlatList
  // still reads as a hierarchy without an explicit tree component.
  const indentFor = useCallback((relPath: string): number => {
    return relPath.split("/").length - 1;
  }, []);

  const renderableFiles = useMemo(
    () => files.filter((entry) => !entry.isDirectory),
    [files],
  );

  // Header buttons — Save is the loud one; New / Refresh are quieter.
  const HeaderActions = (
    <View style={styles.headerActions}>
      <Pressable
        onPress={promptNewFile}
        style={[styles.btnSecondary, { borderColor: c.border }]}
      >
        <Text style={{ color: c.textPrimary, fontWeight: "500" }}>+ New</Text>
      </Pressable>
      <Pressable
        onPress={() => void refreshFiles()}
        style={[styles.btnSecondary, { borderColor: c.border, marginLeft: 6 }]}
      >
        <Text style={{ color: c.textMuted, fontWeight: "500" }}>↻</Text>
      </Pressable>
      <Pressable
        onPress={save}
        disabled={!dirty || saving}
        style={[
          styles.btnPrimary,
          {
            backgroundColor: dirty ? c.accent : c.bgCard,
            borderColor: c.border,
            marginLeft: 8,
            opacity: saving ? 0.7 : 1,
          },
        ]}
      >
        {saving ? (
          <ActivityIndicator color={c.bg} />
        ) : (
          <Text style={{ color: dirty ? c.bg : c.textMuted, fontWeight: "600" }}>
            {dirty ? "Save" : "Saved"}
          </Text>
        )}
      </Pressable>
    </View>
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 8 }}>
      <View style={styles.headerRow}>
        <AppBackButton
          onPress={() => {
            if (dirty) {
              Alert.alert(
                "Unsaved changes",
                "You have unsaved edits. Discard and leave?",
                [
                  { text: "Cancel", style: "cancel" },
                  {
                    text: "Discard",
                    style: "destructive",
                    onPress: () => router.back(),
                  },
                ],
              );
              return;
            }
            router.back();
          }}
        />
        <View style={{ flex: 1, marginLeft: 8 }}>
          <Text style={[styles.h1, { color: c.textPrimary }]}>Code</Text>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            {slugStr} · {renderableFiles.length} file{renderableFiles.length === 1 ? "" : "s"}
            {dirty ? " · unsaved" : ""}
          </Text>
        </View>
        {HeaderActions}
      </View>

      {showNewFileInline ? (
        <View style={[styles.inlineNewFile, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          <TextInput
            value={newFilePathDraft}
            onChangeText={setNewFilePathDraft}
            placeholder="Relative path in src/ (e.g. screens/Home.tsx)"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <View style={{ flexDirection: "row", marginTop: 8 }}>
            <Pressable
              onPress={submitNewFileInline}
              style={[styles.btnPrimary, { backgroundColor: c.accent, flex: 1 }]}
            >
              <Text style={{ color: c.bg, fontWeight: "600" }}>Create</Text>
            </Pressable>
            <Pressable
              onPress={() => {
                setShowNewFileInline(false);
                setNewFilePathDraft("");
              }}
              style={[styles.btnSecondary, { borderColor: c.border, marginLeft: 8, flex: 1 }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "500" }}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      ) : null}

      <View style={styles.body}>
        <View style={[styles.tree, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {filesLoading ? (
            <ActivityIndicator color={c.textMuted} />
          ) : filesError ? (
            <Text style={{ color: "#ff6b6b", fontSize: 12 }}>{filesError}</Text>
          ) : renderableFiles.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              No files yet. Tap + to add one.
            </Text>
          ) : (
            <ScrollView style={{ flex: 1 }}>
              {renderableFiles.map((entry) => {
                const isOpen = entry.path === openPath;
                return (
                  <Pressable
                    key={entry.path}
                    onPress={() => void openFile(entry.path)}
                    onLongPress={() => deleteFile(entry.path)}
                    style={[
                      styles.fileRow,
                      {
                        paddingLeft: 8 + indentFor(entry.path) * 12,
                        backgroundColor: isOpen ? c.bg : "transparent",
                      },
                    ]}
                  >
                    <Text
                      style={{
                        color: isOpen ? c.textPrimary : c.textSecondary,
                        fontFamily: "Menlo",
                        fontSize: 12,
                        fontWeight: isOpen ? "600" : "400",
                      }}
                      numberOfLines={1}
                    >
                      {entry.path.split("/").pop()}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 8 }}>
                      {entry.size}b
                    </Text>
                  </Pressable>
                );
              })}
            </ScrollView>
          )}
        </View>

        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : undefined}
          style={[styles.editor, { borderColor: c.border, backgroundColor: c.bgCard }]}
        >
          {!openPath ? (
            <View style={styles.editorEmpty}>
              <Text style={{ color: c.textMuted, fontSize: 13 }}>
                Tap a file on the left to start editing.
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                Long-press a file to delete it.
              </Text>
            </View>
          ) : editorLoading ? (
            <View style={styles.editorEmpty}>
              <ActivityIndicator color={c.textMuted} />
            </View>
          ) : (
            <>
              <Text
                style={[styles.editorPath, { color: c.textMuted, borderBottomColor: c.border }]}
                numberOfLines={1}
              >
                {openPath}
                {dirty ? " ●" : ""}
              </Text>
              <TextInput
                value={editorContent}
                onChangeText={setEditorContent}
                multiline
                autoCapitalize="none"
                autoCorrect={false}
                spellCheck={false}
                placeholder="// start typing..."
                placeholderTextColor={c.textMuted}
                style={[
                  styles.editorInput,
                  { color: c.textPrimary, backgroundColor: c.bg },
                ]}
                textAlignVertical="top"
              />
            </>
          )}
        </KeyboardAvoidingView>
      </View>

      <View style={{ paddingBottom: insets.bottom }} />
    </View>
  );
}

const styles = StyleSheet.create({
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingBottom: 8,
  },
  h1: { fontSize: 18, fontWeight: "700" },
  headerActions: {
    flexDirection: "row",
    alignItems: "center",
  },
  btnPrimary: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
    alignItems: "center",
    minWidth: 64,
  },
  btnSecondary: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
    alignItems: "center",
  },
  inlineNewFile: {
    marginHorizontal: 12,
    padding: 10,
    borderWidth: 1,
    borderRadius: 8,
    marginBottom: 4,
  },
  input: { borderWidth: 1, borderRadius: 6, padding: 8, fontSize: 13 },
  body: {
    flex: 1,
    flexDirection: "row",
    paddingHorizontal: 12,
    paddingTop: 4,
  },
  tree: {
    width: 140,
    borderWidth: 1,
    borderRadius: 8,
    paddingVertical: 6,
    paddingHorizontal: 4,
    marginRight: 8,
  },
  fileRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: 6,
    paddingRight: 6,
    borderRadius: 4,
  },
  editor: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    overflow: "hidden",
  },
  editorEmpty: { flex: 1, alignItems: "center", justifyContent: "center", padding: 20 },
  editorPath: {
    fontSize: 11,
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderBottomWidth: 1,
    fontFamily: "Menlo",
  },
  editorInput: {
    flex: 1,
    padding: 10,
    fontFamily: "Menlo",
    fontSize: 13,
  },
});
