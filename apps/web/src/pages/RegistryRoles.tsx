import { useQuery } from '@tanstack/react-query'
import { useAuth } from '@/lib/auth-context'
import { fetchRoles } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Skeleton } from '@/components/ui/skeleton'

interface RoleRow {
  id: string
  name: string
  version: string
  status: string
  category: string
}

export function RegistryRoles() {
  const { token } = useAuth()

  const { data, isLoading, error } = useQuery({
    queryKey: ['roles'],
    queryFn: () => fetchRoles(token!),
    enabled: !!token,
  })

  const roles = (data as RoleRow[] | undefined) ?? []

  return (
    <div className="min-h-screen bg-muted/40 p-4">
      <div className="mx-auto max-w-4xl space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold">Registry: Роли</h1>
          <Button size="sm">Создать роль</Button>
        </div>

        {isLoading && (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        )}

        {error && (
          <p className="rounded-md border border-destructive bg-destructive/10 p-4 text-sm text-destructive">
            Ошибка загрузки ролей
          </p>
        )}

        {!isLoading && !error && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Имя</TableHead>
                <TableHead>Версия</TableHead>
                <TableHead>Статус</TableHead>
                <TableHead>Категория</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {roles.length === 0 ? (
                <TableRow>
                  <td colSpan={4} className="text-center text-muted-foreground">
                    Роли не найдены
                  </td>
                </TableRow>
              ) : (
                roles.map((role) => (
                  <TableRow key={role.id}>
                    <td className="font-medium">{role.name}</td>
                    <td>{role.version}</td>
                    <td>{role.status}</td>
                    <td>{role.category}</td>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        )}
      </div>
    </div>
  )
}
