import { useState } from 'react'
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Skeleton } from '@/components/ui/skeleton'
import { useAuth } from '@/lib/auth-context'
import { SDLC_STATES, isLegalTransition, type RunStatus } from '@/lib/sdlc'
import { listRuns, recordTransition, type Run } from '@/lib/tasks'
import { BoardColumn } from './BoardColumn'

export function KanbanBoard() {
  const { token } = useAuth()
  const queryClient = useQueryClient()
  const [activeRunId, setActiveRunId] = useState<string | null>(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['board'],
    queryFn: () => listRuns(token!),
    enabled: !!token,
  })

  const transition = useMutation({
    mutationFn: ({ runId, to }: { runId: string; to: RunStatus }) =>
      recordTransition(token!, runId, to),
    onSuccess: (updated) => {
      queryClient.setQueryData<Run[]>(['board'], (previous) =>
        (previous ?? []).map((run) => (run.id === updated.id ? updated : run)),
      )
    },
  })

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  )

  const runs = data ?? []
  const activeRun = runs.find((run) => run.id === activeRunId) ?? null

  function handleDragStart(event: DragStartEvent) {
    transition.reset()
    setActiveRunId(String(event.active.id))
  }

  function handleDragEnd(event: DragEndEvent) {
    setActiveRunId(null)
    const run = runs.find((item) => item.id === String(event.active.id))
    const target = event.over?.id
    if (!run || !target || run.status === target) {
      return
    }
    // The client rejects undocumented transitions (see lib/tasks.ts); the
    // board surfaces that rejection instead of moving the card.
    transition.mutate({ runId: run.id, to: target as RunStatus })
  }

  if (isLoading) {
    return (
      <div className="space-y-2" data-testid="board-loading">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    )
  }

  if (error) {
    return (
      <p
        role="alert"
        className="rounded-md border border-destructive bg-destructive/10 p-4 text-sm text-destructive"
      >
        Failed to load the board
      </p>
    )
  }

  return (
    <div className="space-y-4">
      {transition.error && (
        <p
          role="alert"
          className="rounded-md border border-destructive bg-destructive/10 p-4 text-sm text-destructive"
        >
          Transition rejected: {transition.error.message}
        </p>
      )}
      <DndContext
        sensors={sensors}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
        onDragCancel={() => setActiveRunId(null)}
      >
        <div className="flex gap-3 overflow-x-auto pb-4">
          {SDLC_STATES.map((state) => (
            <BoardColumn
              key={state.id}
              state={state}
              runs={runs.filter((run) => run.status === state.id)}
              disabled={
                activeRun !== null &&
                activeRun.status !== state.id &&
                !isLegalTransition(activeRun.status, state.id)
              }
            />
          ))}
        </div>
      </DndContext>
    </div>
  )
}
