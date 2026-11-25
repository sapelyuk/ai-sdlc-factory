import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  FIXTURE_RUNS,
  createFixtureBoardClient,
  createHttpBoardClient,
  listRuns,
  type Run,
} from '@/lib/tasks'
import { useAuthStore } from '@/store/auth'

const IN_DEV_RUN: Run = {
  id: 'run-test',
  name: 'Board fixture',
  status: 'in_dev',
  updated_at: '2026-11-24T10:00:00Z',
}

describe('fixture board client', () => {
  it('lists the seeded runs', async () => {
    const client = createFixtureBoardClient()

    const runs = await client.listRuns('token')

    expect(runs).toEqual(FIXTURE_RUNS)
  })

  it('is exposed to components through the default seam', async () => {
    const runs = await listRuns('token')

    expect(runs).toEqual(FIXTURE_RUNS)
  })

  it('records a documented transition and updates the run', async () => {
    const client = createFixtureBoardClient([IN_DEV_RUN])

    const updated = await client.recordTransition('token', IN_DEV_RUN.id, 'code_review')

    expect(updated.id).toBe(IN_DEV_RUN.id)
    expect(updated.status).toBe('code_review')
    expect(updated.updated_at).not.toBe(IN_DEV_RUN.updated_at)

    const runs = await client.listRuns('token')
    expect(runs[0].status).toBe('code_review')
  })

  it('rejects an undocumented transition without moving the run', async () => {
    const client = createFixtureBoardClient([IN_DEV_RUN])

    await expect(client.recordTransition('token', IN_DEV_RUN.id, 'qa')).rejects.toThrow(
      'Illegal transition: in_dev -> qa',
    )

    const runs = await client.listRuns('token')
    expect(runs[0].status).toBe('in_dev')
  })

  it('rejects a transition for an unknown run', async () => {
    const client = createFixtureBoardClient([IN_DEV_RUN])

    await expect(client.recordTransition('token', 'missing', 'code_review')).rejects.toThrow(
      'Unknown run: missing',
    )
  })
})

describe('HTTP board client', () => {
  beforeEach(() => {
    useAuthStore.getState().setAuth('expired-token', {
      id: '1',
      email: 'admin@example.com',
      role: 'admin',
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useAuthStore.getState().clearAuth()
  })

  it('fetches runs with the bearer token', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify([IN_DEV_RUN]), { status: 200 }),
      )
    vi.stubGlobal('fetch', fetchMock)

    const runs = await createHttpBoardClient('/api').listRuns('token-1')

    expect(runs).toEqual([IN_DEV_RUN])
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/runs', {
      headers: { Authorization: 'Bearer token-1' },
    })
  })

  it('clears auth when listing runs responds with 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    await expect(createHttpBoardClient('/api').listRuns('expired-token')).rejects.toThrow('HTTP 401')
    expect(useAuthStore.getState().token).toBeNull()
    expect(useAuthStore.getState().user).toBeNull()
  })

  it('propagates other list errors without clearing auth', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 500 })))

    await expect(createHttpBoardClient('/api').listRuns('expired-token')).rejects.toThrow('HTTP 500')
    expect(useAuthStore.getState().token).toBe('expired-token')
  })

  it('defaults to the /api base URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await createHttpBoardClient().listRuns('token-1')

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/runs', {
      headers: { Authorization: 'Bearer token-1' },
    })
  })

  it('posts a transition and returns the updated run', async () => {
    const updated = { ...IN_DEV_RUN, status: 'code_review' as const }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(updated), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await createHttpBoardClient('/api').recordTransition(
      'token-1',
      IN_DEV_RUN.id,
      'code_review',
    )

    expect(result).toEqual(updated)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/runs/run-test/transitions', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: 'Bearer token-1',
      },
      body: JSON.stringify({ to: 'code_review' }),
    })
  })

  it('clears auth when a transition responds with 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    await expect(
      createHttpBoardClient('/api').recordTransition('expired-token', IN_DEV_RUN.id, 'code_review'),
    ).rejects.toThrow('HTTP 401')
    expect(useAuthStore.getState().token).toBeNull()
  })

  it('propagates other transition errors without clearing auth', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 422 })))

    await expect(
      createHttpBoardClient('/api').recordTransition('expired-token', IN_DEV_RUN.id, 'code_review'),
    ).rejects.toThrow('HTTP 422')
    expect(useAuthStore.getState().token).toBe('expired-token')
  })
})
