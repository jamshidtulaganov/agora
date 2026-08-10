export interface ArtifactRepoRef {
  repo: string;
  branch?: string;
  base_sha: string;
  head_sha: string;
  merge_status: string;
}

export interface IssueArtifactSummary {
  id: string;
  run_id: string;
  step_id: string;
  step_key: string;
  title: string;
  kind: string;
  capability: string;
  canonical: boolean;
  repos: ArtifactRepoRef[];
  completed_at?: string;
}

export interface IssueArtifactResponse {
  run_id: string;
  run_status: string;
  ready: boolean;
  reason?: string;
  artifact?: IssueArtifactSummary;
  components: IssueArtifactSummary[];
  daemon_url: string;
  capabilities: Record<string, string>;
}

export interface ArtifactChangedFile {
  path: string;
  additions: number;
  deletions: number;
}

export interface ArtifactRepoChanges {
  repo: string;
  base_sha: string;
  head_sha: string;
  files: ArtifactChangedFile[];
  tree: string[];
  diff: string;
  truncated: boolean;
}

export interface ArtifactChangesResponse {
  artifact_id: string;
  repos: ArtifactRepoChanges[];
}

export interface ArtifactFileResponse {
  repo: string;
  path: string;
  head_sha: string;
  content: string;
  encoding: string;
  size: number;
  binary: boolean;
  truncated: boolean;
}

export interface ArtifactPreviewResponse {
  artifact_id: string;
  command?: string;
  running: boolean;
  port?: number;
  url?: string;
  proxy_path?: string;
  needs_command: boolean;
  error?: string;
  log?: string;
  configuration_source?: string;
  starting?: boolean;
  ready?: boolean;
}

export interface ArtifactChecksResponse {
  artifact_id: string;
  head_sha: string;
  command?: string;
  exit_code?: number;
  passed?: boolean;
  output: string;
  error?: string;
  needs_command: boolean;
}
