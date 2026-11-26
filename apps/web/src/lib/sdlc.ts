/**
 * SDLC state model for the board.
 *
 * Ground truth: `packages/contracts/state-machine/sdlc_transitions.json`,
 * which mirrors the `runs.status` CHECK constraint in
 * `migrations/postgres/0002_init_tasks_and_sdlc.up.sql` and
 * `docs/architecture/SDLC_STATE_MACHINE.md` sections 2 and 3. Do not add,
 * remove or rename states or transitions here: change the contract and the
 * migration together.
 */

import sdlcContract from '../../../../packages/contracts/state-machine/sdlc_transitions.json'

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

export const SDLC_STATES: readonly SdlcState[] = sdlcContract.states.map((state) => ({
  id: state.id as RunStatus,
  label: state.label,
}))

const STATE_LABELS = new Map<string, string>(SDLC_STATES.map((state) => [state.id, state.label]))

export function isKnownStatus(value: string): value is RunStatus {
  return STATE_LABELS.has(value)
}

/** Display label for a status; unknown values are returned unchanged. */
export function stateLabel(status: string): string {
  return STATE_LABELS.get(status) ?? status
}

/**
 * Legal transitions from `packages/contracts/state-machine/sdlc_transitions.json`
 * (SDLC_STATE_MACHINE.md section 3). The contract has exactly one outgoing
 * transition per state, including the Monitoring -> Research feedback loop, so
 * a source-to-target map is sufficient.
 */
const legalTransitions: Partial<Record<RunStatus, RunStatus>> = {}
for (const transition of sdlcContract.transitions) {
  legalTransitions[transition.from as RunStatus] = transition.to as RunStatus
}

export const LEGAL_TRANSITIONS: Readonly<Partial<Record<RunStatus, RunStatus>>> = legalTransitions

export function isLegalTransition(from: RunStatus, to: RunStatus): boolean {
  return LEGAL_TRANSITIONS[from] === to
}
