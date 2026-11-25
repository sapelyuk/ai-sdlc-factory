import { describe, expect, it } from 'vitest'
import {
  LEGAL_TRANSITIONS,
  SDLC_STATES,
  isKnownStatus,
  isLegalTransition,
  stateLabel,
} from '@/lib/sdlc'

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

  it('allows only the four transitions documented in section 3', () => {
    expect(LEGAL_TRANSITIONS).toEqual({
      in_dev: 'code_review',
      code_review: 'qa',
      qa: 'build',
      release: 'deploy',
    })

    expect(isLegalTransition('in_dev', 'code_review')).toBe(true)
    expect(isLegalTransition('code_review', 'qa')).toBe(true)
    expect(isLegalTransition('qa', 'build')).toBe(true)
    expect(isLegalTransition('release', 'deploy')).toBe(true)
  })

  it('rejects every undocumented transition', () => {
    expect(isLegalTransition('idea', 'research')).toBe(false)
    expect(isLegalTransition('in_dev', 'qa')).toBe(false)
    expect(isLegalTransition('code_review', 'build')).toBe(false)
    expect(isLegalTransition('deploy', 'monitoring')).toBe(false)
    expect(isLegalTransition('monitoring', 'research')).toBe(false)
    expect(isLegalTransition('qa', 'qa')).toBe(false)
  })
})
