// Policy Agent — fleet watchdog. Per-agent speed + flagged tasks (stalled /
// failed / looping) over agent_task_queue, surfaced read-only in the Policy view.

export interface PolicyAgentSpeed {
  agent_id: string;
  agent_name: string;
  task_count: number;
  failed_count: number;
  avg_run_seconds: number;
  p95_run_seconds: number;
  avg_queue_seconds: number;
}

export interface PolicyStalledTask {
  task_id: string;
  agent_id: string;
  agent_name: string;
  issue_id: string;
  started_at: string | null;
  attempt: number;
}

export interface PolicyFailedTask {
  task_id: string;
  agent_id: string;
  agent_name: string;
  issue_id: string;
  /** 'agent_error' | 'timeout' | 'runtime_offline' | 'runtime_recovery' | 'manual' | '' */
  failure_reason: string;
  error: string;
  started_at: string | null;
  completed_at: string | null;
  attempt: number;
}

export interface PolicyLoopingIssue {
  issue_id: string;
  task_count: number;
  last_task_at: string | null;
}

export interface PolicyFleetHealth {
  stall_minutes: number;
  loop_threshold: number;
  agents: PolicyAgentSpeed[];
  stalled: PolicyStalledTask[];
  recent_failures: PolicyFailedTask[];
  looping: PolicyLoopingIssue[];
}
