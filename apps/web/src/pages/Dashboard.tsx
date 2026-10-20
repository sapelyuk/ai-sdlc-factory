import { useAuth } from '@/lib/auth-context'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Link } from 'react-router-dom'

export function Dashboard() {
  const { user, logout } = useAuth()

  return (
    <div className="min-h-screen bg-muted/40 p-4">
      <div className="mx-auto max-w-4xl space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold">ai-sdlc-factory</h1>
          <Button variant="outline" onClick={logout}>
            Выйти
          </Button>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Добро пожаловать</CardTitle>
            <CardDescription>{user?.email} ({user?.role})</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-muted-foreground">
              Это каркас Web UI платформы ai-sdlc-factory.
            </p>
            <div className="flex gap-2 pt-2">
              <Link to="/registry/roles">
                <Button>Роли</Button>
              </Link>
              <Link to="/registry/teams">
                <Button variant="outline">Команды</Button>
              </Link>
              <Link to="/registry/processes">
                <Button variant="outline">Процессы</Button>
              </Link>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
