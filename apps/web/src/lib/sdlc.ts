/**
 * SDLC state model for the board.
 *
 * Ground truth: the `runs.status` CHECK constraint in
 * `migrations/postgres/0002_init_tasks_and_sdlc.up.sql` and
 * `docs/architecture/SDLC_STATE_MACHINE.md` section 2. Do not add, remove or
 * rename states without changing both.
 */

export type RunStatus =
  | 'idea'
  | 'research'
  | 'requirements'
  | 'requirements_review'
  | 'design'
  | 'architecture'
  | 'architecture_review'
  | 'planning'
  | 'in_dev'
  | 'code_review'
  | 'qa'
  | 'build'
  | 'release'
  | 'deploy'
  | 'monitoring'

export interface SdlcState {
  id: RunStatus
  label: string
}

export const SDLC_STATES: readonly SdlcState[] = [
  { id: 'idea', label: 'Idea' },
  { id: 'research', label: 'Research' },
  { id: 'requirements', label: 'Requirements' },
  { id: 'requirements_review', label: 'RequirementsReview' },
  { id: 'design', label: 'Design' },
  { id: 'architecture', label: 'Architecture' },
  { id: 'architecture_review', label: 'ArchitectureReview' },
  { id: 'planning', label: 'Planning' },
  { id: 'in_dev', label: 'InDev' },
  { id: 'code_review', label: 'CodeReview' },
  { id: 'qa', label: 'QA' },
  { id: 'build', label: 'Build' },
  { id: 'release', label: 'Release' },
  { id: 'deploy', label: 'Deploy' },
  { id: 'monitoring', label: 'Monitoring' },
]

const STATE_LABELS = new Map<string, string>(SDLC_STATES.map((state) => [state.id, state.label]))

export function isKnownStatus(value: string): value is RunStatus {
  return STATE_LABELS.has(value)
}

/** Display label for a status; unknown values are returned unchanged. */
export function stateLabel(status: string): string {
  return STATE_LABELS.get(status) ?? status
}

/**
 * Transitions documented in SDLC_STATE_MACHINE.md section 3
 * (3.9 InDev -> CodeReview, 3.10 CodeReview -> QA, 3.12 QA -> Build,
 * 3.15 Release -> Deploy). Every other transition is not allowed on the board.
 */
export const LEGAL_TRANSITIONS: Readonly<Partial<Record<RunStatus, RunStatus>>> = {
  in_dev: 'code_review',
  code_review: 'qa',
  qa: 'build',
  release: 'deploy',
}

export function isLegalTransition(from: RunStatus, to: RunStatus): boolean {
  return LEGAL_TRANSITIONS[from] === to
}
