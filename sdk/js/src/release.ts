export interface ReleaseIdentity {
  packageName?: string;
  bundleId?: string;
  productId?: string;
  track?: string;
}

export interface ReleasePostcondition {
  kind: string;
  value?: string;
}

export interface ReleaseTarget {
  id: string;
  label?: string;
  kind: string;
  workDir?: string;
  publishCommand?: string;
  artifactGlobs?: string[];
  requiresConfirmation?: boolean;
  identity?: ReleaseIdentity;
  postconditions?: ReleasePostcondition[];
}

export interface ReleasePlan {
  project: string;
  projectDir: string;
  target: ReleaseTarget;
  workDir: string;
  command: string;
  mutatesExternalState: boolean;
  confirmation?: string;
  credentialRefs?: Record<string, string>;
  artifactGlobs?: string[];
  postconditions?: ReleasePostcondition[];
  missingCredentials?: string[];
  warnings?: string[];
}

export interface ReleaseProof {
  kind: string;
  ok: boolean;
  remoteId?: string;
  detail?: string;
  verifiedAt: string;
}

export interface ReleaseRun {
  id: string;
  project?: string;
  projectDir: string;
  targetId: string;
  targetKind: string;
  provider: string;
  status: 'running' | 'completed' | 'failed' | 'dispatched';
  workDir?: string;
  command?: string;
  message?: string;
  error?: string;
  artifacts?: Array<{
    name: string;
    path: string;
    bucket?: string;
    key?: string;
    publicUrl?: string;
    sha256?: string;
  }>;
  proofs?: ReleaseProof[];
  startedAt: string;
  finishedAt?: string;
}

export interface StartReleaseOptions {
  dir: string;
  target: string;
  /** Exact token returned by plan(). Never infer or auto-fill this for users. */
  confirmation?: string;
  allowGitHubFallback?: boolean;
}

type Requester = <T>(path: string, init?: RequestInit) => Promise<T>;

/** Typed client for Yaver's project-scoped release broker. */
export class YaverReleaseClient {
  constructor(private readonly request: Requester) {}

  plan(dir: string, target = ''): Promise<ReleasePlan> {
    const query = new URLSearchParams({ dir });
    if (target) query.set('target', target);
    return this.request<ReleasePlan>(`/publish/plan?${query.toString()}`);
  }

  start(options: StartReleaseOptions): Promise<ReleaseRun> {
    return this.request<ReleaseRun>('/publish/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(options),
    });
  }

  list(): Promise<ReleaseRun[]> {
    return this.request<ReleaseRun[]>('/publish/runs');
  }

  status(runId: string): Promise<ReleaseRun> {
    return this.request<ReleaseRun>(`/publish/runs/${encodeURIComponent(runId)}`);
  }
}
