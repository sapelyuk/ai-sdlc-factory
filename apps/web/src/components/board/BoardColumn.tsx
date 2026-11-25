import { useDroppable } from '@dnd-kit/core'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { SdlcState } from '@/lib/sdlc'
import type { Run } from '@/lib/tasks'
import { cn } from '@/lib/utils'
import { TaskCard } from './TaskCard'

interface BoardColumnProps {
  state: SdlcState
  runs: Run[]
  /** True when the column is not a legal drop target for the dragged run. */
  disabled: boolean
}

export function BoardColumn({ state, runs, disabled }: BoardColumnProps) {
  const { setNodeRef, isOver } = useDroppable({ id: state.id, disabled })

  return (
    <Card
      ref={setNodeRef}
      data-testid={`column-${state.id}`}
      aria-disabled={disabled}
      className={cn(
        'w-60 shrink-0 transition-colors',
        disabled && 'opacity-50',
        isOver && 'ring-2 ring-primary',
      )}
    >
      <CardHeader className="flex-row items-center justify-between space-y-0 p-3">
        <CardTitle className="text-sm">{state.label}</CardTitle>
        <span
          data-testid={`count-${state.id}`}
          className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground"
        >
          {runs.length}
        </span>
      </CardHeader>
      <CardContent className="space-y-2 p-3 pt-0">
        {runs.length === 0 ? (
          <p className="text-xs text-muted-foreground">No runs</p>
        ) : (
          runs.map((run) => <TaskCard key={run.id} run={run} />)
        )}
      </CardContent>
    </Card>
  )
}
