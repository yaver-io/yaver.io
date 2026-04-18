import * as FileSystem from "expo-file-system/legacy";

export interface DesignImportResult {
  sourceType: "figma" | "screenshot" | "reference-link";
  provider?: DesignProvider;
  sourceUrl: string;
  previewUrl?: string;
  fileKey?: string;
  nodeId?: string;
  fileName: string;
  nodeName: string;
  nodeType: string;
  pageName?: string;
  colors: string[];
  topLevelLayers: string[];
  textSnippets: string[];
  summary: string;
}

export type DesignProvider =
  | "figma"
  | "canva"
  | "framer"
  | "miro"
  | "dribbble"
  | "behance"
  | "generic";

export interface DesignProviderSpec {
  id: DesignProvider;
  label: string;
  placeholder: string;
  helper: string;
}

export const DESIGN_PROVIDERS: DesignProviderSpec[] = [
  {
    id: "canva",
    label: "Canva",
    placeholder: "https://www.canva.com/design/...",
    helper:
      "Use a Canva share link or export a screen as an image. Yaver will treat it as a structured reference and carry provider hints into the implementation brief.",
  },
  {
    id: "framer",
    label: "Framer",
    placeholder: "https://your-site.framer.website or share link",
    helper:
      "Use a Framer published page or share link. Best for landing pages and web UI references.",
  },
  {
    id: "miro",
    label: "Miro",
    placeholder: "https://miro.com/app/board/...",
    helper:
      "Use a Miro board link when the design source is a board, flow, or whiteboard rather than polished screens.",
  },
  {
    id: "dribbble",
    label: "Dribbble",
    placeholder: "https://dribbble.com/shots/...",
    helper:
      "Use a Dribbble shot link as style reference or interaction inspiration.",
  },
  {
    id: "behance",
    label: "Behance",
    placeholder: "https://www.behance.net/gallery/...",
    helper:
      "Use a Behance project link for multi-screen or case-study style references.",
  },
  {
    id: "generic",
    label: "Generic",
    placeholder: "Any design reference URL",
    helper:
      "Fallback for any other design tool, moodboard, deck, or shared reference page.",
  },
];

interface FigmaNode {
  id?: string;
  name?: string;
  type?: string;
  children?: FigmaNode[];
  characters?: string;
  fills?: Array<{ visible?: boolean; type?: string; color?: { r: number; g: number; b: number } }>;
}

function extractFileKey(url: string): string | null {
  const match = url.match(/figma\.com\/(?:file|design|proto)\/([a-zA-Z0-9]+)/);
  return match?.[1] ?? null;
}

function extractNodeId(url: string): string | null {
  const match = url.match(/[?&]node-id=([^&#]+)/);
  return match ? decodeURIComponent(match[1]) : null;
}

function hexFromColor(nodeColor?: { r: number; g: number; b: number } | null): string | null {
  if (!nodeColor) return null;
  const toHex = (value: number) =>
    Math.max(0, Math.min(255, Math.round(value * 255)))
      .toString(16)
      .padStart(2, "0")
      .toUpperCase();
  return `#${toHex(nodeColor.r)}${toHex(nodeColor.g)}${toHex(nodeColor.b)}`;
}

function walkFigma(
  node: FigmaNode | undefined,
  depth: number,
  layers: string[],
  texts: string[],
  colors: string[],
) {
  if (!node) return;
  if (depth <= 1 && node.name) layers.push(node.name);
  if (node.type === "TEXT" && typeof node.characters === "string" && node.characters.trim()) {
    texts.push(node.characters.trim());
  }
  for (const fill of node.fills ?? []) {
    if (fill?.visible === false || fill?.type !== "SOLID") continue;
    const hex = hexFromColor(fill.color);
    if (hex) colors.push(hex);
  }
  for (const child of node.children ?? []) {
    walkFigma(child, depth + 1, layers, texts, colors);
  }
}

function uniqueCompact(items: string[], limit: number): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    const normalized = item.trim();
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
    if (out.length >= limit) break;
  }
  return out;
}

function summarizeImport(data: {
  provider?: DesignProvider;
  fileName: string;
  nodeName: string;
  nodeType: string;
  pageName?: string;
  topLevelLayers: string[];
  textSnippets: string[];
  colors: string[];
}) {
  const parts = [
    `Imported Figma node "${data.nodeName}" (${data.nodeType}) from file "${data.fileName}".`,
  ];
  if (data.provider && data.provider !== "figma" && data.provider !== "generic") {
    parts.unshift(`Reference provider: ${data.provider}.`);
  }
  if (data.pageName) parts.push(`Page: ${data.pageName}.`);
  if (data.topLevelLayers.length) parts.push(`Top layers: ${data.topLevelLayers.join(", ")}.`);
  if (data.textSnippets.length) parts.push(`Visible text: ${data.textSnippets.join(" | ")}.`);
  if (data.colors.length) parts.push(`Palette clues: ${data.colors.join(", ")}.`);
  return parts.join(" ");
}

export async function importFigmaReference(url: string, token: string): Promise<DesignImportResult> {
  const sourceUrl = url.trim();
  const apiToken = token.trim();
  if (!sourceUrl) throw new Error("Figma URL is required");
  if (!apiToken) throw new Error("Figma access token is required");

  const fileKey = extractFileKey(sourceUrl);
  if (!fileKey) throw new Error("Could not extract Figma file key from URL");
  const nodeId = extractNodeId(sourceUrl) ?? undefined;

  const headers = {
    "X-Figma-Token": apiToken,
  };

  const fileRes = await fetch(`https://api.figma.com/v1/files/${fileKey}`, { headers });
  const fileJson = await fileRes.json().catch(() => ({}));
  if (!fileRes.ok) {
    throw new Error(fileJson?.err || `Figma file fetch failed (${fileRes.status})`);
  }

  let targetNode: FigmaNode | undefined;
  let pageName = "";

  if (nodeId) {
    const nodesRes = await fetch(
      `https://api.figma.com/v1/files/${fileKey}/nodes?ids=${encodeURIComponent(nodeId)}&depth=4`,
      { headers },
    );
    const nodesJson = await nodesRes.json().catch(() => ({}));
    if (!nodesRes.ok) {
      throw new Error(nodesJson?.err || `Figma node fetch failed (${nodesRes.status})`);
    }
    targetNode = nodesJson?.nodes?.[nodeId]?.document as FigmaNode | undefined;
  } else {
    const document = fileJson?.document as FigmaNode | undefined;
    pageName = document?.children?.[0]?.name ?? "";
    targetNode = document?.children?.[0] ?? document;
  }

  if (!targetNode) throw new Error("Could not load the selected Figma node");

  if (!pageName) {
    const root = fileJson?.document as FigmaNode | undefined;
    for (const page of root?.children ?? []) {
      const found = (function find(node?: FigmaNode): boolean {
        if (!node) return false;
        if (node.id === nodeId) return true;
        return (node.children ?? []).some((child) => find(child));
      })(page);
      if (found) {
        pageName = page.name ?? "";
        break;
      }
    }
  }

  const layerNames: string[] = [];
  const textSnippets: string[] = [];
  const colors: string[] = [];
  walkFigma(targetNode, 0, layerNames, textSnippets, colors);

  let previewUrl: string | undefined;
  const previewNodeId = targetNode.id || nodeId;
  if (previewNodeId) {
    const imageRes = await fetch(
      `https://api.figma.com/v1/images/${fileKey}?ids=${encodeURIComponent(previewNodeId)}&format=png&scale=2`,
      { headers },
    );
    const imageJson = await imageRes.json().catch(() => ({}));
    if (imageRes.ok) {
      previewUrl = imageJson?.images?.[previewNodeId];
    }
  }

  const compactLayers = uniqueCompact(layerNames, 8);
  const compactTexts = uniqueCompact(textSnippets, 6).map((item) =>
    item.length > 80 ? `${item.slice(0, 77)}...` : item,
  );
  const compactColors = uniqueCompact(colors, 8);

  const result: DesignImportResult = {
    sourceType: "figma",
    provider: "figma",
    sourceUrl,
    fileKey,
    nodeId: previewNodeId,
    fileName: String(fileJson?.name || "Untitled Figma file"),
    nodeName: targetNode.name || "Unnamed node",
    nodeType: targetNode.type || "UNKNOWN",
    pageName: pageName || undefined,
    previewUrl,
    colors: compactColors,
    topLevelLayers: compactLayers,
    textSnippets: compactTexts,
    summary: summarizeImport({
      fileName: String(fileJson?.name || "Untitled Figma file"),
      nodeName: targetNode.name || "Unnamed node",
      nodeType: targetNode.type || "UNKNOWN",
      provider: "figma",
      pageName: pageName || undefined,
      topLevelLayers: compactLayers,
      textSnippets: compactTexts,
      colors: compactColors,
    }),
  };
  return result;
}

export async function generateDesignModeBrief(args: {
  apiKey: string;
  imported: DesignImportResult;
  productGoal: string;
  targetSurface: "mobile-ui" | "web-ui" | "full-stack";
}): Promise<string> {
  const key = args.apiKey.trim();
  if (!key) throw new Error("OpenAI/Codex-compatible API key is required");
  const contents: Array<{ type: string; text?: string; image_url?: string }> = [
    {
      type: "input_text",
      text: [
        `Target surface: ${args.targetSurface}.`,
        `Reference source type: ${args.imported.sourceType}.`,
        `Reference provider: ${args.imported.provider ?? "unknown"}.`,
        `Product goal: ${args.productGoal.trim() || "Turn the imported design into a working UI implementation."}`,
        `Design summary: ${args.imported.summary}`,
        `Top-level layers: ${args.imported.topLevelLayers.join(", ") || "none"}`,
        `Text snippets: ${args.imported.textSnippets.join(" | ") || "none"}`,
        `Palette clues: ${args.imported.colors.join(", ") || "none"}`,
        "Return markdown with sections: Goal, Information Architecture, Components, Visual System, States, and Build Order.",
      ].join("\n"),
    },
  ];
  if (args.imported.previewUrl) {
    contents.push({
      type: "input_image",
      image_url: args.imported.previewUrl,
    });
  }

  const res = await fetch("https://api.openai.com/v1/responses", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: "gpt-4.1-mini",
      input: [
        {
          role: "system",
          content: [
            {
              type: "input_text",
              text:
                "You are a product design implementation planner. Write a compact but actionable UI build brief for a coding agent. Focus on information architecture, components, states, spacing, hierarchy, palette, copy, and implementation priorities. Be concrete and implementation-ready.",
            },
          ],
        },
        {
          role: "user",
          content: contents,
        },
      ],
    }),
  });
  const text = await res.text().catch(() => "");
  if (!res.ok) throw new Error(text || `OpenAI HTTP ${res.status}`);
  const json = JSON.parse(text) as { output_text?: string };
  if (typeof json.output_text === "string" && json.output_text.trim()) return json.output_text.trim();
  throw new Error("The AI provider returned an empty brief");
}

export function buildRemoteDesignPrompt(args: {
  imported: DesignImportResult;
  brief?: string;
  targetSurface: "mobile-ui" | "web-ui" | "full-stack";
  productGoal: string;
}) {
  const parts = [
    `Implement a ${args.targetSurface} based on the imported Figma reference.`,
    `Reference provider: ${args.imported.provider ?? "unknown"}`,
    `Goal: ${args.productGoal.trim() || "Turn the design into working UI."}`,
    `Figma URL: ${args.imported.sourceUrl}`,
    `File: ${args.imported.fileName}`,
    `Selected node: ${args.imported.nodeName} (${args.imported.nodeType})`,
    args.imported.pageName ? `Page: ${args.imported.pageName}` : "",
    args.imported.topLevelLayers.length ? `Top-level layers: ${args.imported.topLevelLayers.join(", ")}` : "",
    args.imported.textSnippets.length ? `Visible text snippets: ${args.imported.textSnippets.join(" | ")}` : "",
    args.imported.colors.length ? `Palette clues: ${args.imported.colors.join(", ")}` : "",
    "Inspect the current project first, then implement the closest high-quality version of this design in the existing stack.",
    "Preserve established repo patterns where they exist. If the design conflicts with the current structure, choose the smallest coherent implementation that gets the core flow working.",
    args.brief ? `Implementation brief:\n${args.brief}` : "",
  ];
  return parts.filter(Boolean).join("\n\n");
}

export async function importScreenshotReference(imageUri: string): Promise<DesignImportResult> {
  if (!imageUri.trim()) throw new Error("Screenshot URI is required");
  const base64 = await FileSystem.readAsStringAsync(imageUri, {
    encoding: FileSystem.EncodingType.Base64,
  });
  const previewUrl = `data:image/jpeg;base64,${base64}`;
  return {
    sourceType: "screenshot",
    provider: "generic",
    sourceUrl: imageUri,
    previewUrl,
    fileName: "Imported screenshot",
    nodeName: "Screenshot reference",
    nodeType: "IMAGE",
    colors: [],
    topLevelLayers: [],
    textSnippets: [],
    summary:
      "Imported a screenshot reference. Use the image itself as the primary visual source of truth for layout, hierarchy, spacing, and style.",
  };
}

export function importReferenceLink(args: {
  url: string;
  provider?: DesignProvider;
  label?: string;
  notes?: string;
}): DesignImportResult {
  const sourceUrl = args.url.trim();
  if (!sourceUrl) throw new Error("Reference URL is required");
  const label = args.label?.trim() || "Reference board";
  const notes = args.notes?.trim() || "";
  const provider = args.provider ?? detectDesignProvider(sourceUrl);
  return {
    sourceType: "reference-link",
    provider,
    sourceUrl,
    fileName: label,
    nodeName: label,
    nodeType: "REFERENCE",
    colors: [],
    topLevelLayers: [],
    textSnippets: notes ? [notes] : [],
    summary: notes
      ? `Imported a ${provider} design reference link: ${label}. Notes: ${notes}`
      : `Imported a ${provider} design reference link: ${label}.`,
  };
}

export function detectDesignProvider(url: string): DesignProvider {
  const value = url.toLowerCase();
  if (value.includes("figma.com")) return "figma";
  if (value.includes("canva.com")) return "canva";
  if (value.includes("framer.") || value.includes("framer.com")) return "framer";
  if (value.includes("miro.com")) return "miro";
  if (value.includes("dribbble.com")) return "dribbble";
  if (value.includes("behance.net")) return "behance";
  return "generic";
}
