export type EdgeTaskKind =
  | "speech-transcription"
  | "ocr"
  | "vision-labeling"
  | "embedding"
  | "rerank"
  | "small-local-agent"
  | "batch-preprocessing"
  | "big-llm-chat"
  | "long-context-reasoning";

export type EdgeDevice = {
  deviceId: string;
  name: string;
  platform: string;
  isOnline: boolean;
  needsAuth?: boolean;
  runnerDown?: boolean;
  deviceClass?: "desktop" | "edge-mobile" | "server";
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
    preferredTasks: Array<"speech" | "ocr" | "vision" | "embedding" | "rerank" | "automation" | "small-llm">;
    memoryMb?: number;
    batteryPct?: number;
    isCharging?: boolean;
    thermalState?: "nominal" | "warm" | "hot";
  };
};

type EdgePreferredTask =
  NonNullable<EdgeDevice["edgeProfile"]>["preferredTasks"][number];

type PlacementRecommendation = {
  mode: "edge" | "infra" | "hybrid";
  rationale: string;
  candidateDevices: Array<{
    deviceId: string;
    name: string;
    platform: string;
    score: number;
    notes: string[];
  }>;
  farming: {
    worthwhile: boolean;
    rationale: string;
    suggestedTaskKinds: EdgeTaskKind[];
  };
};

const TASK_TO_PROFILE_TAG: Record<string, EdgePreferredTask | undefined> = {
  "speech-transcription": "speech",
  "ocr": "ocr",
  "vision-labeling": "vision",
  "embedding": "embedding",
  "rerank": "rerank",
  "small-local-agent": "small-llm",
  "batch-preprocessing": "automation",
};

function inferDeviceClass(device: EdgeDevice): "desktop" | "edge-mobile" | "server" {
  if (device.deviceClass) return device.deviceClass;
  if (device.platform === "android" || device.platform === "ios") return "edge-mobile";
  return "desktop";
}

function scoreDeviceForTask(device: EdgeDevice, taskKind: EdgeTaskKind) {
  const notes: string[] = [];
  if (!device.isOnline) return { score: -100, notes: ["offline"] };
  if (device.needsAuth) return { score: -100, notes: ["needs auth"] };
  if (device.runnerDown) return { score: -100, notes: ["runner unhealthy"] };

  let score = inferDeviceClass(device) === "edge-mobile" ? 20 : 10;
  const profile = device.edgeProfile;
  if (!profile) {
    if (inferDeviceClass(device) === "edge-mobile") {
      notes.push("mobile device without profile; treating as light edge worker");
    }
    return { score, notes };
  }

  if (!profile.supportsLocalInference) {
    score -= 40;
    notes.push("local inference disabled");
  } else {
    score += 10;
  }

  const preferredTag = TASK_TO_PROFILE_TAG[taskKind];
  if (preferredTag && profile.preferredTasks.includes(preferredTag)) {
    score += 25;
    notes.push(`preferred for ${preferredTag}`);
  }

  if (profile.thermalState === "warm") {
    score -= 10;
    notes.push("thermals warm");
  } else if (profile.thermalState === "hot") {
    score -= 40;
    notes.push("thermally constrained");
  }

  if (typeof profile.batteryPct === "number") {
    if (profile.batteryPct < 25 && !profile.isCharging) {
      score -= 25;
      notes.push("battery low");
    } else if (profile.batteryPct >= 60 || profile.isCharging) {
      score += 10;
    }
  }

  if (taskKind === "small-local-agent") {
    if (profile.maxModelClass === "small" || profile.maxModelClass === "medium") {
      score += 20;
    } else {
      score -= 20;
      notes.push("small local agent likely too heavy");
    }
  }

  if (taskKind === "big-llm-chat" || taskKind === "long-context-reasoning") {
    score -= 60;
    notes.push("large reasoning belongs on infra");
  }

  return { score, notes };
}

export function recommendPlacement(devices: EdgeDevice[], taskKind: EdgeTaskKind): PlacementRecommendation {
  const eligible = devices
    .map((device) => {
      const { score, notes } = scoreDeviceForTask(device, taskKind);
      return {
        deviceId: device.deviceId,
        name: device.name,
        platform: device.platform,
        score,
        notes,
        className: inferDeviceClass(device),
      };
    })
    .filter((device) => device.score > -50)
    .sort((a, b) => b.score - a.score);

  const bestEdge = eligible.find((device) => device.className === "edge-mobile" && device.score >= 25);
  const edgePool = eligible.filter((device) => device.className === "edge-mobile" && device.score >= 20);

  if (taskKind === "big-llm-chat" || taskKind === "long-context-reasoning") {
    return {
      mode: "infra",
      rationale: "Large-context or deep-reasoning tasks are memory-bandwidth bound and are a poor fit for phone hardware.",
      candidateDevices: eligible.slice(0, 3).map(({ className: _className, ...device }) => device),
      farming: {
        worthwhile: false,
        rationale: "A phone farm is not an efficient substitute for GPU-backed LLM inference.",
        suggestedTaskKinds: [],
      },
    };
  }

  if (taskKind === "batch-preprocessing") {
    return {
      mode: edgePool.length >= 2 ? "edge" : "hybrid",
      rationale: edgePool.length >= 2
        ? "Background preprocessing can be spread across idle phones when jobs are independent and latency does not matter."
        : "Batch preprocessing can use edge workers, but a single device pool is thin enough that infra should remain available as spillover.",
      candidateDevices: eligible.slice(0, 5).map(({ className: _className, ...device }) => device),
      farming: {
        worthwhile: edgePool.length >= 2,
        rationale: edgePool.length >= 2
          ? "Phone farms are viable for embarrassingly parallel work such as OCR batches, embeddings, and media preprocessing."
          : "You need multiple healthy edge devices before farming becomes useful.",
        suggestedTaskKinds: ["batch-preprocessing", "ocr", "embedding", "speech-transcription", "vision-labeling"],
      },
    };
  }

  if (bestEdge && ["speech-transcription", "ocr", "vision-labeling", "embedding", "rerank"].includes(taskKind)) {
    return {
      mode: "edge",
      rationale: "This task is lightweight, parallel-friendly, and benefits from running close to the capture device.",
      candidateDevices: eligible.slice(0, 5).map(({ className: _className, ...device }) => device),
      farming: {
        worthwhile: edgePool.length >= 2,
        rationale: edgePool.length >= 2
          ? "A pool of old phones can help on small edge-model workloads."
          : "Useful on one phone already; farming only helps if you have many independent jobs.",
        suggestedTaskKinds: ["speech-transcription", "ocr", "vision-labeling", "embedding", "rerank"],
      },
    };
  }

  if (bestEdge && taskKind === "small-local-agent") {
    return {
      mode: "hybrid",
      rationale: "Use the phone for local preprocessing or short-context steps, but escalate planning and heavy reasoning to infra.",
      candidateDevices: eligible.slice(0, 5).map(({ className: _className, ...device }) => device),
      farming: {
        worthwhile: false,
        rationale: "Phone farms are still weak for agentic LLM execution because synchronization and thermals dominate.",
        suggestedTaskKinds: ["speech-transcription", "ocr", "embedding"],
      },
    };
  }

  return {
    mode: "infra",
    rationale: "No healthy edge device is a strong match for this task, so infra should stay the primary execution target.",
    candidateDevices: eligible.slice(0, 5).map(({ className: _className, ...device }) => device),
    farming: {
      worthwhile: false,
      rationale: "Farming only makes sense for independent, low-latency-insensitive jobs.",
      suggestedTaskKinds: ["batch-preprocessing", "ocr", "embedding"],
    },
  };
}
