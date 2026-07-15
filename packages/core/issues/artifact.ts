import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import type {
  ArtifactChangesResponse,
  ArtifactChecksResponse,
  ArtifactFileResponse,
  ArtifactPreviewResponse,
} from "../types";

const ArtifactChangedFileSchema = z.object({
  path: z.string(),
  additions: z.number().int().nonnegative().default(0),
  deletions: z.number().int().nonnegative().default(0),
}).strip();

const ArtifactRepoChangesSchema = z.object({
  repo: z.string(),
  base_sha: z.string(),
  head_sha: z.string(),
  files: z.array(ArtifactChangedFileSchema).default([]),
  tree: z.array(z.string()).default([]),
  diff: z.string().default(""),
  truncated: z.boolean().default(false),
}).strip();

const ArtifactChangesResponseSchema = z.object({
  artifact_id: z.string(),
  repos: z.array(ArtifactRepoChangesSchema).default([]),
}).strip();

const ArtifactFileResponseSchema = z.object({
  repo: z.string(),
  path: z.string(),
  head_sha: z.string(),
  content: z.string().default(""),
  encoding: z.string().default("utf-8"),
  size: z.number().nonnegative().default(0),
  binary: z.boolean().default(false),
  truncated: z.boolean().default(false),
}).strip();

const ArtifactPreviewResponseSchema = z.object({
  artifact_id: z.string(),
  command: z.string().optional(),
  running: z.boolean().default(false),
  port: z.number().int().positive().optional(),
  url: z.string().optional(),
  proxy_path: z.string().optional(),
  needs_command: z.boolean().default(false),
  error: z.string().optional(),
  log: z.string().optional(),
}).strip();

const ArtifactChecksResponseSchema = z.object({
  artifact_id: z.string(),
  head_sha: z.string().default(""),
  command: z.string().optional(),
  exit_code: z.number().int().optional(),
  passed: z.boolean().optional(),
  output: z.string().default(""),
  error: z.string().optional(),
  needs_command: z.boolean().default(false),
}).strip();

const EMPTY_ARTIFACT_CHANGES: ArtifactChangesResponse = {
  artifact_id: "",
  repos: [],
};

const EMPTY_ARTIFACT_FILE: ArtifactFileResponse = {
  repo: "",
  path: "",
  head_sha: "",
  content: "",
  encoding: "utf-8",
  size: 0,
  binary: false,
  truncated: false,
};

const EMPTY_ARTIFACT_PREVIEW: ArtifactPreviewResponse = {
  artifact_id: "",
  running: false,
  needs_command: false,
};

const EMPTY_ARTIFACT_CHECKS: ArtifactChecksResponse = {
  artifact_id: "",
  head_sha: "",
  output: "",
  needs_command: false,
};

export function parseArtifactChangesResponse(value: unknown): ArtifactChangesResponse {
  return parseWithFallback(
    value,
    ArtifactChangesResponseSchema,
    EMPTY_ARTIFACT_CHANGES,
    { endpoint: "POST /artifact/changes" },
  );
}

export function parseArtifactFileResponse(value: unknown): ArtifactFileResponse {
  return parseWithFallback(
    value,
    ArtifactFileResponseSchema,
    EMPTY_ARTIFACT_FILE,
    { endpoint: "POST /artifact/file" },
  );
}

export function parseArtifactPreviewResponse(value: unknown): ArtifactPreviewResponse {
  return parseWithFallback(
    value,
    ArtifactPreviewResponseSchema,
    EMPTY_ARTIFACT_PREVIEW,
    { endpoint: "POST /artifact/preview" },
  );
}

export function parseArtifactChecksResponse(value: unknown): ArtifactChecksResponse {
  return parseWithFallback(
    value,
    ArtifactChecksResponseSchema,
    EMPTY_ARTIFACT_CHECKS,
    { endpoint: "POST /artifact/checks" },
  );
}
