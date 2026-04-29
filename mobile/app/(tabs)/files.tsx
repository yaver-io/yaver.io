import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Dimensions,
  FlatList,
  Image,
  Linking,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import Markdown from "react-native-markdown-display";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// Read-only file browser for the "I want to peek at a repo from
// my couch" use case. Scoped server-side to the agent's
// discovered project roots — we can't escape the sandbox.

interface FileRoot {
  id: string;
  name: string;
  path: string;
}

interface FileEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  mtime: number;
}

export default function FilesScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [currentRoot, setCurrentRoot] = useState<FileRoot | null>(null);
  const [currentPath, setCurrentPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [fileName, setFileName] = useState<string>("");
  const [binary, setBinary] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadRoots = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`${quicClient.baseUrl}/files/roots`, {
        headers: quicClient.getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setRoots(data.roots || []);
    } catch (e: any) {
      setError(e?.message ?? "failed to load roots");
    } finally {
      setLoading(false);
    }
  }, [connected]);

  const loadDirectory = useCallback(
    async (root: FileRoot, path: string) => {
      setLoading(true);
      setError(null);
      setFileContent(null);
      setBinary(false);
      try {
        const url = `${quicClient.baseUrl}/files/list?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(path)}`;
        const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        setEntries(data.entries || []);
        setCurrentRoot(root);
        setCurrentPath(path);
      } catch (e: any) {
        setError(e?.message ?? "failed to list");
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const openFile = useCallback(async (root: FileRoot, path: string) => {
    setLoading(true);
    setError(null);
    setFileName(path.split("/").pop() || path);
    const ext = (path.split(".").pop() || "").toLowerCase();
    // Image files: don't fetch raw bytes as JSON — load via <Image> with auth header URL
    if (["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"].includes(ext)) {
      setFileContent(`__IMAGE__:${root.id}:${path}`);
      setBinary(false);
      setLoading(false);
      return;
    }
    try {
      const url = `${quicClient.baseUrl}/files/read?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(path)}`;
      const res = await fetch(url, { headers: quicClient.getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (data.binary) {
        setBinary(true);
        setFileContent(null);
      } else {
        setFileContent(data.content ?? "");
        setBinary(false);
      }
    } catch (e: any) {
      setError(e?.message ?? "failed to read");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadRoots();
  }, [loadRoots]);

  const up = useCallback(() => {
    if (fileContent != null || binary) {
      setFileContent(null);
      setBinary(false);
      setFileName("");
      return;
    }
    if (currentPath === "") {
      setCurrentRoot(null);
      setEntries([]);
      return;
    }
    const parent = currentPath.split("/").slice(0, -1).join("/");
    if (currentRoot) loadDirectory(currentRoot, parent);
  }, [fileContent, binary, currentPath, currentRoot, loadDirectory]);

  const goHome = useCallback(() => {
    setCurrentRoot(null);
    setCurrentPath("");
    setEntries([]);
    setFileContent(null);
    setBinary(false);
  }, []);

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader
        title="Files"
        onBack={() => router.navigate("/(tabs)/more" as any)}
        style={{ paddingTop: insets.top + 12 }}
        right={
          <Pressable onPress={goHome} style={{ paddingVertical: 8 }}>
            <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Home</Text>
          </Pressable>
        }
      />

      {currentRoot ? (
        <View style={[styles.crumbs, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
          <Pressable onPress={up} style={styles.upBtn}>
            <Text style={[styles.upBtnIcon, { color: c.accent }]}>{"\u2190"}</Text>
          </Pressable>
          <View style={{ flex: 1 }}>
            <Text style={[styles.crumbProject, { color: c.textPrimary }]} numberOfLines={1}>
              {currentRoot.name}
            </Text>
            {currentPath ? (
              <Text style={[styles.crumbPath, { color: c.textMuted }]} numberOfLines={1}>
                {currentPath.split("/").join("  /  ")}
              </Text>
            ) : (
              <Text style={[styles.crumbPath, { color: c.textMuted }]}>root</Text>
            )}
          </View>
        </View>
      ) : null}

      {error ? (
        <View style={[styles.errorBar, { backgroundColor: "#fee2e2" }]}>
          <Text style={{ color: "#dc2626", fontSize: 13, fontWeight: "500" }}>{error}</Text>
        </View>
      ) : null}

      {loading ? (
        <View style={styles.centered}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : fileContent != null ? (
        <FileViewer
          name={fileName}
          content={fileContent}
          currentRoot={currentRoot}
          colors={c}
        />
      ) : binary ? (
        <View style={styles.centered}>
          <Text style={{ fontSize: 48 }}>{"\u{1F4E6}"}</Text>
          <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 12 }]}>Binary file — cannot preview</Text>
        </View>
      ) : currentRoot ? (
        <FlatList
          data={[...entries].sort((a, b) => {
            if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
            return a.name.localeCompare(b.name);
          })}
          keyExtractor={(e) => e.path}
          refreshControl={
            <RefreshControl refreshing={loading} onRefresh={() => loadDirectory(currentRoot, currentPath)} tintColor={c.textMuted} />
          }
          contentContainerStyle={{ paddingVertical: 6 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() =>
                item.isDir
                  ? loadDirectory(currentRoot, item.path)
                  : openFile(currentRoot, item.path)
              }
              style={({ pressed }) => [
                styles.row,
                { borderBottomColor: c.border, backgroundColor: pressed ? c.bgCard : "transparent" },
              ]}
            >
              <View style={[styles.iconWrap, { backgroundColor: item.isDir ? "#818cf822" : c.bgCard }]}>
                <Text style={{ fontSize: 18 }}>{fileEmoji(item)}</Text>
              </View>
              <View style={{ flex: 1, marginRight: 12 }}>
                <Text style={[styles.name, { color: c.textPrimary }]} numberOfLines={1}>
                  {item.name}
                </Text>
                <Text style={[styles.meta, { color: c.textMuted }]} numberOfLines={1}>
                  {item.isDir ? "Folder" : humanSize(item.size)} {item.mtime ? `\u00B7 ${relativeTime(item.mtime)}` : ""}
                </Text>
              </View>
              <Text style={[styles.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={
            <View style={styles.centered}>
              <Text style={{ fontSize: 42 }}>{"\u{1F4C2}"}</Text>
              <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 10 }]}>Empty folder</Text>
            </View>
          }
        />
      ) : (
        <FlatList
          data={roots}
          keyExtractor={(r) => r.id}
          refreshControl={<RefreshControl refreshing={loading} onRefresh={loadRoots} tintColor={c.textMuted} />}
          contentContainerStyle={{ padding: 12 }}
          renderItem={({ item }) => (
            <Pressable
              onPress={() => loadDirectory(item, "")}
              style={({ pressed }) => [
                styles.projectCard,
                { backgroundColor: pressed ? c.bgCardElevated || c.bg : c.bgCard, borderColor: c.border },
              ]}
            >
              <View style={[styles.projectIcon, { backgroundColor: "#818cf822" }]}>
                <Text style={{ fontSize: 22 }}>{"\u{1F4C1}"}</Text>
              </View>
              <View style={{ flex: 1 }}>
                <Text style={[styles.projectName, { color: c.textPrimary }]} numberOfLines={1}>
                  {item.name}
                </Text>
                <Text style={[styles.projectPath, { color: c.textMuted }]} numberOfLines={1}>
                  {item.path}
                </Text>
              </View>
              <Text style={[styles.chevron, { color: c.textMuted }]}>{"\u203A"}</Text>
            </Pressable>
          )}
          ListEmptyComponent={
            <View style={styles.centered}>
              <Text style={{ fontSize: 42 }}>{"\u{1F50D}"}</Text>
              <Text style={[styles.emptyText, { color: c.textMuted, marginTop: 10, textAlign: "center", paddingHorizontal: 40 }]}>
                No projects discovered yet. The agent scans your home directory automatically.
              </Text>
            </View>
          }
        />
      )}
    </View>
  );
}

// FileViewer renders content appropriately per file type:
// - Markdown → rendered markdown with react-native-markdown-display
// - Images → <Image>
// - CSV → table
// - JSON → pretty-printed with syntax colors
// - Code (ts/js/go/py/etc.) → monospace with keyword highlighting
// - Logs / plain text → monospace, word-wrap
function FileViewer({
  name, content, currentRoot, colors,
}: {
  name: string;
  content: string;
  currentRoot: FileRoot | null;
  colors: any;
}) {
  const ext = (name.split(".").pop() || "").toLowerCase();
  const win = Dimensions.get("window");

  // Image viewer
  if (content.startsWith("__IMAGE__:") && currentRoot) {
    const [, rootId, path] = content.split(":", 3);
    const src = `${(quicClient as any).baseUrl}/files/raw?root=${encodeURIComponent(rootId)}&path=${encodeURIComponent(path)}`;
    const authHeaders = (quicClient as any).authHeaders || {};
    return (
      <ScrollView contentContainerStyle={styles.imageWrap}>
        <Image
          source={{ uri: src, headers: authHeaders }}
          style={{ width: win.width - 24, height: win.width - 24, resizeMode: "contain" }}
        />
      </ScrollView>
    );
  }

  // Markdown viewer
  if (ext === "md" || ext === "mdx" || ext === "markdown") {
    const mdStyles = {
      body: { color: colors.textPrimary, fontSize: 15, lineHeight: 22 },
      heading1: { color: colors.textPrimary, fontSize: 26, fontWeight: "700", marginTop: 12, marginBottom: 8 },
      heading2: { color: colors.textPrimary, fontSize: 22, fontWeight: "700", marginTop: 16, marginBottom: 6 },
      heading3: { color: colors.textPrimary, fontSize: 18, fontWeight: "700", marginTop: 14, marginBottom: 4 },
      paragraph: { color: colors.textPrimary, fontSize: 15, lineHeight: 22, marginTop: 4, marginBottom: 8 },
      strong: { fontWeight: "700", color: colors.textPrimary },
      em: { fontStyle: "italic", color: colors.textPrimary },
      code_inline: { fontFamily: "Menlo", fontSize: 13, backgroundColor: colors.bgCard, color: colors.accent, paddingHorizontal: 4, borderRadius: 4 },
      code_block: { fontFamily: "Menlo", fontSize: 12, backgroundColor: colors.bgCard, color: colors.textPrimary, padding: 10, borderRadius: 8 },
      fence: { fontFamily: "Menlo", fontSize: 12, backgroundColor: colors.bgCard, color: colors.textPrimary, padding: 10, borderRadius: 8, marginVertical: 6 },
      link: { color: colors.accent, textDecorationLine: "underline" },
      blockquote: { borderLeftWidth: 3, borderLeftColor: colors.accent, paddingLeft: 10, marginVertical: 8, color: colors.textMuted },
      bullet_list: { marginVertical: 4 },
      ordered_list: { marginVertical: 4 },
      list_item: { color: colors.textPrimary, marginVertical: 2 },
      hr: { backgroundColor: colors.border, height: 1, marginVertical: 12 },
      table: { borderWidth: 1, borderColor: colors.border, borderRadius: 6, marginVertical: 8 },
      th: { padding: 8, backgroundColor: colors.bgCard, fontWeight: "700" },
      td: { padding: 8, borderTopWidth: 1, borderColor: colors.border },
    } as any;
    return (
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 60 }}>
        <Markdown style={mdStyles} onLinkPress={(url) => { Linking.openURL(url).catch(() => {}); return false; }}>
          {content}
        </Markdown>
      </ScrollView>
    );
  }

  // CSV / TSV viewer (table)
  if (ext === "csv" || ext === "tsv") {
    const sep = ext === "tsv" ? "\t" : ",";
    const rows = content.split(/\r?\n/).filter(Boolean).slice(0, 500).map((ln) => parseCSVLine(ln, sep));
    const colCount = Math.max(...rows.map(r => r.length));
    return (
      <ScrollView horizontal contentContainerStyle={{ padding: 10 }}>
        <View>
          {rows.map((row, i) => (
            <View key={i} style={{ flexDirection: "row", backgroundColor: i === 0 ? colors.bgCard : "transparent" }}>
              {Array.from({ length: colCount }).map((_, j) => (
                <View key={j} style={[styles.tdCell, { borderColor: colors.border, minWidth: 120 }]}>
                  <Text style={{ color: colors.textPrimary, fontWeight: i === 0 ? "700" : "400", fontSize: 13 }}>
                    {row[j] ?? ""}
                  </Text>
                </View>
              ))}
            </View>
          ))}
        </View>
      </ScrollView>
    );
  }

  // JSON viewer (pretty-print + color keys)
  if (ext === "json") {
    let pretty = content;
    try { pretty = JSON.stringify(JSON.parse(content), null, 2); } catch {}
    return (
      <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 80 }}>
        <Text style={{ fontFamily: "Menlo", fontSize: 12, lineHeight: 18 }}>
          {highlightJSON(pretty, colors)}
        </Text>
      </ScrollView>
    );
  }

  // .docx / .xlsx / .pdf — show "open externally" hint
  if (["docx", "doc", "xlsx", "xls", "pdf", "pptx", "ppt"].includes(ext)) {
    return (
      <View style={styles.centered}>
        <Text style={{ fontSize: 44 }}>{ext === "pdf" ? "\u{1F4D1}" : "\u{1F4C4}"}</Text>
        <Text style={[styles.emptyText, { color: colors.textPrimary, marginTop: 12, fontWeight: "600" }]}>{name}</Text>
        <Text style={[styles.emptyText, { color: colors.textMuted, marginTop: 4 }]}>
          {ext.toUpperCase()} preview not available in app.
        </Text>
        <Text style={[styles.emptyText, { color: colors.textMuted, marginTop: 2, fontSize: 12 }]}>
          Download & open with a system viewer from your dev machine.
        </Text>
      </View>
    );
  }

  // Code files — syntax-aware highlighting (keywords + strings + comments)
  const codeExts = ["ts", "tsx", "js", "jsx", "go", "py", "rs", "rb", "swift", "kt", "java", "c", "cpp", "h", "hpp", "cs", "php", "sh", "bash", "sql", "html", "css", "scss", "yaml", "yml", "toml", "xml"];
  if (codeExts.includes(ext)) {
    return (
      <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 80 }}>
        <Text style={[styles.code, { color: colors.textPrimary }]}>
          {highlightCode(content, ext, colors)}
        </Text>
      </ScrollView>
    );
  }

  // Plain text
  return (
    <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 80 }}>
      <Text style={[styles.code, { color: colors.textPrimary }]}>{content}</Text>
    </ScrollView>
  );
}

// --- Simple CSV line parser handling quoted commas.
function parseCSVLine(line: string, sep: string): string[] {
  const out: string[] = [];
  let cur = "";
  let inQ = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (inQ) {
      if (ch === '"' && line[i + 1] === '"') { cur += '"'; i++; }
      else if (ch === '"') inQ = false;
      else cur += ch;
    } else {
      if (ch === '"') inQ = true;
      else if (ch === sep) { out.push(cur); cur = ""; }
      else cur += ch;
    }
  }
  out.push(cur);
  return out;
}

// --- JSON syntax highlighter → React children.
function highlightJSON(src: string, colors: any): React.ReactNode[] {
  // Tokenize: strings (including keys), numbers, booleans/null, punctuation.
  const parts: React.ReactNode[] = [];
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}\[\],])/g;
  let last = 0; let m; let k = 0;
  while ((m = re.exec(src)) !== null) {
    if (m.index > last) parts.push(src.slice(last, m.index));
    if (m[1]) {
      const isKey = !!m[2];
      parts.push(<Text key={k++} style={{ color: isKey ? "#eab308" : "#22c55e" }}>{m[1]}</Text>);
      if (m[2]) parts.push(m[2]);
    } else if (m[3]) {
      parts.push(<Text key={k++} style={{ color: "#3b82f6" }}>{m[3]}</Text>);
    } else if (m[4]) {
      parts.push(<Text key={k++} style={{ color: "#a855f7" }}>{m[4]}</Text>);
    } else {
      parts.push(<Text key={k++} style={{ color: colors.textMuted }}>{m[5]}</Text>);
    }
    last = re.lastIndex;
  }
  if (last < src.length) parts.push(src.slice(last));
  return parts;
}

// --- Lightweight code highlighter. Not a full parser — just language-agnostic
// keyword / string / comment coloring. Good enough for readability.
const LANG_KEYWORDS: Record<string, string[]> = {
  ts: ["import", "export", "from", "const", "let", "var", "function", "return", "if", "else", "for", "while", "async", "await", "class", "interface", "type", "enum", "new", "this", "true", "false", "null", "undefined", "try", "catch", "throw", "extends", "implements", "public", "private", "protected", "static", "readonly"],
  tsx: ["import", "export", "from", "const", "let", "var", "function", "return", "if", "else", "for", "while", "async", "await", "class", "interface", "type", "enum", "new", "this", "true", "false", "null", "undefined", "try", "catch", "throw", "extends", "implements"],
  js: ["import", "export", "from", "const", "let", "var", "function", "return", "if", "else", "for", "while", "async", "await", "class", "new", "this", "true", "false", "null", "undefined", "try", "catch", "throw", "extends"],
  jsx: ["import", "export", "from", "const", "let", "var", "function", "return", "if", "else", "class", "new", "this"],
  go: ["package", "import", "func", "return", "var", "const", "type", "struct", "interface", "if", "else", "for", "range", "switch", "case", "default", "go", "defer", "chan", "select", "map", "make", "true", "false", "nil", "break", "continue"],
  py: ["def", "class", "import", "from", "as", "return", "if", "elif", "else", "for", "while", "try", "except", "finally", "with", "yield", "raise", "pass", "break", "continue", "lambda", "and", "or", "not", "in", "is", "True", "False", "None"],
  rs: ["fn", "let", "mut", "const", "pub", "use", "struct", "enum", "impl", "trait", "if", "else", "for", "while", "loop", "match", "return", "async", "await", "true", "false", "mod", "crate", "self", "Self"],
  swift: ["import", "func", "let", "var", "class", "struct", "enum", "protocol", "extension", "if", "else", "for", "in", "while", "return", "async", "await", "throws", "try", "catch", "guard", "self", "true", "false", "nil", "public", "private", "internal", "override"],
  kt: ["fun", "val", "var", "class", "object", "interface", "import", "return", "if", "else", "for", "while", "when", "true", "false", "null", "override", "private", "public", "internal"],
  java: ["public", "private", "protected", "static", "final", "class", "interface", "extends", "implements", "import", "package", "new", "this", "return", "if", "else", "for", "while", "try", "catch", "throw", "true", "false", "null"],
  sh: ["if", "then", "else", "fi", "for", "in", "do", "done", "while", "function", "return", "export", "local", "case", "esac"],
  yaml: [],
  yml: [],
  html: [],
  css: [],
};

function highlightCode(src: string, ext: string, colors: any): React.ReactNode[] {
  const keywords = new Set(LANG_KEYWORDS[ext] || []);
  const isCLike = ["ts", "tsx", "js", "jsx", "go", "rs", "swift", "kt", "java", "c", "cpp", "cs", "php", "scss", "css"].includes(ext);
  const isPy = ext === "py";
  const isSh = ext === "sh" || ext === "bash";

  // Tokenize greedily: comments > strings > numbers > identifiers > other.
  const parts: React.ReactNode[] = [];
  let k = 0;
  let i = 0;
  const len = src.length;

  const pushText = (s: string) => parts.push(s);
  const pushColored = (s: string, color: string) =>
    parts.push(<Text key={k++} style={{ color }}>{s}</Text>);

  while (i < len) {
    const rest = src.slice(i);
    // Line comment
    if (isCLike && rest.startsWith("//")) {
      const end = rest.indexOf("\n");
      const chunk = end < 0 ? rest : rest.slice(0, end);
      pushColored(chunk, colors.textMuted);
      i += chunk.length;
      continue;
    }
    if ((isPy || isSh) && rest.startsWith("#")) {
      const end = rest.indexOf("\n");
      const chunk = end < 0 ? rest : rest.slice(0, end);
      pushColored(chunk, colors.textMuted);
      i += chunk.length;
      continue;
    }
    // Block comment (/* ... */)
    if (isCLike && rest.startsWith("/*")) {
      const end = rest.indexOf("*/");
      const chunk = end < 0 ? rest : rest.slice(0, end + 2);
      pushColored(chunk, colors.textMuted);
      i += chunk.length;
      continue;
    }
    // Strings: " ' ` (no multi-char escape handling beyond \X)
    const ch = src[i];
    if (ch === '"' || ch === "'" || ch === "`") {
      let j = i + 1;
      while (j < len && src[j] !== ch) {
        if (src[j] === "\\") j += 2;
        else j++;
      }
      j = Math.min(j + 1, len);
      pushColored(src.slice(i, j), "#22c55e");
      i = j;
      continue;
    }
    // Numbers
    if (/\d/.test(ch) && (i === 0 || /[^A-Za-z_]/.test(src[i - 1]))) {
      let j = i;
      while (j < len && /[0-9.xXoObB_a-fA-F]/.test(src[j])) j++;
      pushColored(src.slice(i, j), "#3b82f6");
      i = j;
      continue;
    }
    // Identifier
    if (/[A-Za-z_$]/.test(ch)) {
      let j = i;
      while (j < len && /[A-Za-z0-9_$]/.test(src[j])) j++;
      const word = src.slice(i, j);
      if (keywords.has(word)) pushColored(word, "#a855f7");
      else pushText(word);
      i = j;
      continue;
    }
    pushText(ch);
    i++;
  }
  return parts;
}

function humanSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

function relativeTime(ts: number): string {
  const now = Date.now() / 1000;
  const diff = now - ts;
  if (diff < 60) return "just now";
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
  const d = new Date(ts * 1000);
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

function fileEmoji(item: FileEntry): string {
  if (item.isDir) return "\u{1F4C1}";
  const ext = item.name.toLowerCase().split(".").pop() || "";
  const map: Record<string, string> = {
    ts: "\u{1F4D8}", tsx: "\u{1F4D8}", js: "\u{1F4D9}", jsx: "\u{1F4D9}",
    json: "\u{1F4DC}", md: "\u{1F4DD}", yml: "\u2699", yaml: "\u2699",
    go: "\u{1F43A}", rs: "\u{1F980}", py: "\u{1F40D}", rb: "\u{1F48E}",
    swift: "\u{1F9A2}", kt: "\u{1F536}", java: "\u2615",
    sh: "\u{1F4BB}", env: "\u{1F510}", lock: "\u{1F512}",
    png: "\u{1F5BC}", jpg: "\u{1F5BC}", jpeg: "\u{1F5BC}", gif: "\u{1F5BC}", svg: "\u{1F5BC}",
    mp4: "\u{1F3AC}", mov: "\u{1F3AC}",
    zip: "\u{1F4E6}", tar: "\u{1F4E6}", gz: "\u{1F4E6}",
  };
  return map[ext] || "\u{1F4C4}";
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
  crumbs: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderBottomWidth: 1,
    gap: 12,
  },
  upBtn: { width: 34, height: 34, borderRadius: 17, alignItems: "center", justifyContent: "center" },
  upBtnIcon: { fontSize: 20, fontWeight: "600" },
  crumbProject: { fontSize: 15, fontWeight: "700" },
  crumbPath: { fontSize: 11, marginTop: 2 },
  row: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 11,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  iconWrap: {
    width: 36, height: 36, borderRadius: 10,
    alignItems: "center", justifyContent: "center",
    marginRight: 12,
  },
  name: { fontSize: 15, fontWeight: "500" },
  meta: { fontSize: 11, marginTop: 2 },
  chevron: { fontSize: 20, fontWeight: "300" },
  errorBar: { padding: 12 },
  emptyText: { fontSize: 14 },
  centered: { alignItems: "center", justifyContent: "center", paddingVertical: 60, flex: 1 },
  fileScroll: { flex: 1 },
  code: { fontFamily: "Menlo", fontSize: 12, lineHeight: 17 },
  projectCard: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 14,
    borderWidth: 1,
    marginBottom: 10,
  },
  projectIcon: {
    width: 44, height: 44, borderRadius: 12,
    alignItems: "center", justifyContent: "center",
    marginRight: 12,
  },
  projectName: { fontSize: 16, fontWeight: "700" },
  projectPath: { fontSize: 11, marginTop: 2 },
  imageWrap: { alignItems: "center", justifyContent: "center", padding: 12 },
  tdCell: { padding: 10, borderRightWidth: StyleSheet.hairlineWidth, borderBottomWidth: StyleSheet.hairlineWidth },
});
