import { useAuthStore } from '@/store/auth'
import { isLegalTransition, type RunStatus } from '@/lib/sdlc'

export interface Run {
  id: string
  name: string
  status: RunStatus
  updated_at: string
}

/**
 * Board data seam. Components depend only on this interface: the fixture
 * implementation below is active until #163 lands the real API and swaps the
 * default client for the HTTP one, without touching the UI.
 */
export interface BoardClient {
  listRuns(token: string): Promise<Run[]>
  recordTransition(token: string, runId: string, to: RunStatus): Promise<Run>
}

export const FIXTURE_RUNS: readonly Run[] = [
  {
    id: 'run-0001',
    name: 'SDLC kanban board',
    status: 'in_dev',
    updated_at: '2026-11-24T10:00:00Z',
  },
  {
    id: 'run-0002',
    name: 'Run state machine on Temporal',
    status: 'code_review',
    updated_at: '2026-11-24T09:30:00Z',
  },
  {
    id: 'run-0003',
    name: 'Platform API contracts',
    status: 'qa',
    updated_at: '2026-11-23T16:45:00Z',
  },
  {
    id: 'run-0004',
    name: 'Sandbox network policy',
    status: 'release',
    updated_at: '2026-11-22T12:15:00Z',
  },
  {
    id: 'run-0005',
    name: 'Observability baseline',
    status: 'monitoring',
    updated_at: '2026-11-21T08:00:00Z',
  },
  {
    id: 'run-0006',
    name: 'Team registry v2',
    status: 'idea',
    updated_at: '2026-11-20T18:20:00Z',
  },
]

/**
 * In-memory fixture client. A transition is applied only when
 * SDLC_STATE_MACHINE.md section 3 documents it; anything else is rejected and
 * the run keeps its status.
 */
export function createFixtureBoardClient(seed: readonly Run[] = FIXTURE_RUNS): BoardClient {
  const runs: Run[] = seed.map((run) => ({ ...run }))

  return {
    async listRuns(): Promise<Run[]> {
      return runs.map((run) => ({ ...run }))
    },

    async recordTransition(_token: string, runId: string, to: RunStatus): Promise<Run> {
      const run = runs.find((item) => item.id === runId)
      if (!run) {
        throw new Error(`Unknown run: ${runId}`)
      }
      if (!isLegalTransition(run.status, to)) {
        throw new Error(`Illegal transition: ${run.status} -> ${to}`)
      }
      run.status = to
      run.updated_at = new Date().toISOString()
      return { ...run }
    },
  }
}

/**
 * HTTP implementation of the same seam. It is not wired in yet: the runs
 * read/transition endpoints do not exist and their contract lands in #163.
 * Paths are injected by the caller so that no endpoint is hard-coded here
 * before the contract exists.
 */
export interface HttpBoardClientOptions {
  /** Base URL of the platform API; defaults to VITE_API_URL or /api. */
  baseUrl?: string
  /** Path of the runs collection, taken from the #163 contract. */
  runsPath: string
  /** Builds the transition request path for a run, taken from the #163 contract. */
  transitionPath: (runId: string) => string
}

export function createHttpBoardClient(options: HttpBoardClientOptions): BoardClient {
  const base = options.baseUrl ?? (import.meta.env.VITE_API_URL || '/api')

  return {
    async listRuns(token: string): Promise<Run[]> {
      const response = await fetch(`${base}${options.runsPath}`, {
        headers: { Authorization: `Bearer ${token}` },
      })

      if (!response.ok) {
        if (response.status === 401) {
          useAuthStore.getState().clearAuth()
        }
        throw new Error(`HTTP ${response.status}`)
      }

      return response.json()
    },

    async recordTransition(token: string, runId: string, to: RunStatus): Promise<Run> {
      const response = await fetch(`${base}${options.transitionPath(runId)}`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ to }),
      })

      if (!response.ok) {
        if (response.status === 401) {
          useAuthStore.getState().clearAuth()
        }
        throw new Error(`HTTP ${response.status}`)
      }

      return response.json()
    },
  }
}

// Fixture-backed until #163 replaces this line with createHttpBoardClient().
const defaultClient: BoardClient = createFixtureBoardClient()

export function listRuns(token: string): Promise<Run[]> {
  return defaultClient.listRuns(token)
}

export function recordTransition(token: string, runId: string, to: RunStatus): Promise<Run> {
  return defaultClient.recordTransition(token, runId, to)
}
