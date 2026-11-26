import { describe, expect, it } from 'vitest'
import {
  LEGAL_TRANSITIONS,
  SDLC_STATES,
  isKnownStatus,
  isLegalTransition,
  stateLabel,
  type RunStatus,
} from '@/lib/sdlc'
import sdlcContract from '../../../../packages/contracts/state-machine/sdlc_transitions.json'

// Values copied verbatim from the runs.status CHECK constraint in
// migrations/postgres/0002_init_tasks_and_sdlc.up.sql.
const RUN_STATUSES = [
  'idea',
  'research',
  'requirements',
  'requirements_review',
  'design',
  'architecture',
  'architecture_review',
  'planning',
  'in_dev',
  'code_review',
  'qa',
  'build',
  'release',
  'deploy',
  'monitoring',
]

describe('SDLC state model', () => {
  it('lists all 15 run statuses in the documented order', () => {
    expect(SDLC_STATES).toHaveLength(15)
    expect(SDLC_STATES.map((state) => state.id)).toEqual(RUN_STATUSES)
  })

  it('maps every snake_case id to its display label', () => {
    expect(SDLC_STATES.map((state) => [state.id, state.label])).toEqual([
      ['idea', 'Idea'],
      ['research', 'Research'],
      ['requirements', 'Requirements'],
      ['requirements_review', 'RequirementsReview'],
      ['design', 'Design'],
      ['architecture', 'Architecture'],
      ['architecture_review', 'ArchitectureReview'],
      ['planning', 'Planning'],
      ['in_dev', 'InDev'],
      ['code_review', 'CodeReview'],
      ['qa', 'QA'],
      ['build', 'Build'],
      ['release', 'Release'],
      ['deploy', 'Deploy'],
      ['monitoring', 'Monitoring'],
    ])
  })

  it('recognizes known statuses and keeps unknown ones untouched', () => {
    for (const state of SDLC_STATES) {
      expect(isKnownStatus(state.id)).toBe(true)
      expect(stateLabel(state.id)).toBe(state.label)
    }

    expect(isKnownStatus('archived')).toBe(false)
    expect(stateLabel('archived')).toBe('archived')
  })

  it('allows exactly the 15 transitions from the machine-readable contract', () => {
    const expected = Object.fromEntries(
      sdlcContract.transitions.map((transition) => [transition.from, transition.to]),
    )

    expect(LEGAL_TRANSITIONS).toEqual(expected)
    expect(Object.keys(LEGAL_TRANSITIONS)).toHaveLength(15)

    for (const transition of sdlcContract.transitions) {
      expect(isLegalTransition(transition.from as RunStatus, transition.to as RunStatus)).toBe(true)
    }
  })

  it('rejects every transition outside the contract', () => {
    expect(isLegalTransition('idea', 'design')).toBe(false)
    expect(isLegalTransition('research', 'idea')).toBe(false)
    expect(isLegalTransition('in_dev', 'qa')).toBe(false)
    expect(isLegalTransition('code_review', 'build')).toBe(false)
    expect(isLegalTransition('monitoring', 'requirements')).toBe(false)
    expect(isLegalTransition('qa', 'qa')).toBe(false)
  })
})
